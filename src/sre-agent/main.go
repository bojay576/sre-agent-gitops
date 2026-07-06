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

	log.Printf("SRE Agent started namespace=%s poll_interval=%s api_server=%s", cfg.Namespace, cfg.PollInterval, cfg.APIServer)
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		if err := checkPods(context.Background(), client, cfg); err != nil {
			log.Printf("pod check failed: %v", err)
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

func checkPods(ctx context.Context, client *http.Client, cfg config) error {
	url := cfg.APIServer + "/api/v1/namespaces/" + cfg.Namespace + "/pods"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("kubernetes API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var pods podList
	if err := json.Unmarshal(body, &pods); err != nil {
		return err
	}

	notReady := 0
	restarts := 0
	for _, pod := range pods.Items {
		if pod.Status.Phase != "Running" && pod.Status.Phase != "Succeeded" {
			notReady++
			log.Printf("pod attention namespace=%s name=%s phase=%s", pod.Metadata.Namespace, pod.Metadata.Name, pod.Status.Phase)
		}
		for _, status := range pod.Status.ContainerStatuses {
			restarts += status.RestartCount
			if !status.Ready && pod.Status.Phase == "Running" {
				log.Printf("container not ready pod=%s container=%s restarts=%d", pod.Metadata.Name, status.Name, status.RestartCount)
			}
		}
	}

	log.Printf("cluster check namespace=%s pods=%d not_ready=%d restarts=%d", cfg.Namespace, len(pods.Items), notReady, restarts)
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
