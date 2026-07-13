package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const serviceAccountPath = "/var/run/secrets/kubernetes.io/serviceaccount"

type config struct {
	Namespace    string
	PollInterval time.Duration
	APIServer    string
	Token        string
	CACertPath   string
}

type podList struct {
	Items []struct {
		Metadata struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				Name         string `json:"name"`
				RestartCount int    `json:"restartCount"`
				Ready        bool   `json:"ready"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	client, err := kubernetesClient(cfg)
	if err != nil {
		log.Fatal(err)
	}

	// 加载 LLM 配置（不启用不影响运行）
	llm := loadLLMConfig()
	if llm.Provider != "" {
		log.Printf("LLM enabled provider=%s model=%s url=%s", llm.Provider, llm.Model, llm.APIURL)
	} else {
		log.Printf("LLM disabled (set LLM_PROVIDER to enable)")
	}

	remedy := newRemedier(client, cfg)

	log.Printf("SRE Agent started namespace=%s poll_interval=%s api_server=%s",
		cfg.Namespace, cfg.PollInterval, cfg.APIServer)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	// LLM 分析缓存 key=faultKey → lastAnalysisTime
	llmCache := make(map[string]time.Time)
	llmCacheInterval, _ := time.ParseDuration(
		envOrDefault("LLM_CACHE_INTERVAL", "5m"),
	)

	llmTimeout := 60 * time.Second

	for {
		faults := checkPods(context.Background(), client, cfg)

		// 发现故障且 LLM 启用 → 调用大模型分析
		if len(faults) > 0 && llm.Provider != "" {
			// 生成指纹，检查是否刚分析过
			fingerprint := faultFingerprint(faults, llmCacheInterval)
			now := time.Now()

			if last, ok := llmCache[fingerprint]; ok && now.Sub(last) < llmCacheInterval {
				log.Printf("LLM skip: same faults analyzed %v ago (cache=%v)", now.Sub(last).Round(time.Second), llmCacheInterval)
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
				analysis, err := analyzeFaults(ctx, llm, faults)
				cancel()

				if err != nil {
					log.Printf("LLM analysis failed: %v", err)
				} else if analysis != nil {
					remedy.executeActions(context.Background(), analysis)
				}
				llmCache[fingerprint] = now
			}
		}

		<-ticker.C
	}
}

func loadConfig() (config, error) {
	tokenBytes, err := os.ReadFile(filepath.Join(serviceAccountPath, "token"))
	if err != nil {
		return config{}, fmt.Errorf("read service account token: %w", err)
	}

	namespace := envOrDefault("WATCH_NAMESPACE", "default")
	intervalSeconds, err := strconv.Atoi(envOrDefault("POLL_INTERVAL_SECONDS", "30"))
	if err != nil || intervalSeconds < 5 {
		intervalSeconds = 30
	}

	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := envOrDefault("KUBERNETES_SERVICE_PORT", "443")
	apiServer := os.Getenv("KUBERNETES_API_URL")
	if apiServer == "" {
		if host == "" {
			return config{}, fmt.Errorf("KUBERNETES_SERVICE_HOST is empty")
		}
		apiServer = "https://" + host + ":" + port
	}

	return config{
		Namespace:    namespace,
		PollInterval: time.Duration(intervalSeconds) * time.Second,
		APIServer:    strings.TrimRight(apiServer, "/"),
		Token:        strings.TrimSpace(string(tokenBytes)),
		CACertPath:   filepath.Join(serviceAccountPath, "ca.crt"),
	}, nil
}

func kubernetesClient(cfg config) (*http.Client, error) {
	certPool := x509.NewCertPool()
	caCert, err := os.ReadFile(cfg.CACertPath)
	if err != nil {
		return nil, fmt.Errorf("read cluster CA: %w", err)
	}
	if ok := certPool.AppendCertsFromPEM(caCert); !ok {
		return nil, fmt.Errorf("parse cluster CA failed")
	}

	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    certPool,
		}},
	}, nil
}

// checkPods 检查 Pod 状态，返回发现的故障列表（不再直接输出日志）
func checkPods(ctx context.Context, client *http.Client, cfg config) []PodFault {
	url := cfg.APIServer + "/api/v1/namespaces/" + cfg.Namespace + "/pods"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Printf("pod check: create request failed: %v", err)
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("pod check: request failed: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		log.Printf("pod check: read failed: %v", err)
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("pod check: API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		return nil
	}

	var pods podList
	if err := json.Unmarshal(body, &pods); err != nil {
		log.Printf("pod check: parse failed: %v", err)
		return nil
	}

	var faults []PodFault
	notReady := 0
	totalRestarts := 0

	for _, pod := range pods.Items {
		var issues []containerIssue

		// 检查整 Pod 是否异常
		if pod.Status.Phase != "Running" && pod.Status.Phase != "Succeeded" {
			notReady++
			log.Printf("pod attention namespace=%s name=%s phase=%s",
				pod.Metadata.Namespace, pod.Metadata.Name, pod.Status.Phase)
		}

		// 检查每个容器
		for _, status := range pod.Status.ContainerStatuses {
			totalRestarts += status.RestartCount
			if !status.Ready && pod.Status.Phase == "Running" {
				log.Printf("container not ready pod=%s container=%s restarts=%d",
					pod.Metadata.Name, status.Name, status.RestartCount)
				issues = append(issues, containerIssue{
					Container:    status.Name,
					RestartCount: status.RestartCount,
					Ready:        status.Ready,
				})
			}
		}

		// 如果该 Pod 有问题，加入故障列表
		if pod.Status.Phase != "Running" && pod.Status.Phase != "Succeeded" || len(issues) > 0 {
			faults = append(faults, PodFault{
				Namespace: pod.Metadata.Namespace,
				Name:      pod.Metadata.Name,
				Phase:     pod.Status.Phase,
				Issues:    issues,
			})
		}
	}

	log.Printf("cluster check namespace=%s pods=%d not_ready=%d restarts=%d",
		cfg.Namespace, len(pods.Items), notReady, totalRestarts)

	return faults
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// faultFingerprint 生成故障列表的指纹用于 LLM 缓存去重
func faultFingerprint(faults []PodFault, cacheInterval time.Duration) string {
	// 只考虑最近的故障（避免缓存过长导致漏掉新故障）
	if cacheInterval > 30*time.Minute {
		cacheInterval = 5 * time.Minute
	}
	var b strings.Builder
	for _, f := range faults {
		b.WriteString(f.Namespace)
		b.WriteString("/")
		b.WriteString(f.Name)
		b.WriteString("=")
		b.WriteString(f.Phase)
		for _, issue := range f.Issues {
			b.WriteString(";")
			b.WriteString(issue.Container)
			b.WriteString(":")
			b.WriteString(strconv.Itoa(issue.RestartCount))
		}
	}
	return b.String()
}

