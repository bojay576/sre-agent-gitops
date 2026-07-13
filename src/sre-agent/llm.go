package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// LLM 配置
// ---------------------------------------------------------------------------

type llmConfig struct {
	Provider string // "openai" | "ollama" | ""（空 = 不启用）
	APIURL   string // OpenAI: "https://api.openai.com/v1", Ollama: "http://ollama:11434"
	APIKey   string // OpenAI / Anthropic 等云 API 的 key
	Model    string // "gpt-4o-mini" / "claude-sonnet-4-20250514" / "qwen2.5:7b"
}

// ---------------------------------------------------------------------------
// OpenAPI-compatible chat completion (OpenAI / Anthropic / 兼容代理)
// ---------------------------------------------------------------------------

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	Stream      bool            `json:"stream"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// Ollama chat completion
// ---------------------------------------------------------------------------

type ollamaRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	Stream      bool            `json:"stream"`
	Format      string          `json:"format,omitempty"` // "json"
}

type ollamaResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done  bool   `json:"done"`
	Error string `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// 故障报告 → LLM → 分析结果
// ---------------------------------------------------------------------------

// PodFault 描述单个 Pod 的问题
type PodFault struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Issues    []containerIssue `json:"issues,omitempty"`
}

type containerIssue struct {
	Container   string `json:"container"`
	RestartCount int   `json:"restart_count"`
	Ready       bool   `json:"ready"`
}

// LLMAnalysis LLM 返回的结构化分析结果
type LLMAnalysis struct {
	Analysis    string       `json:"analysis"`
	RootCause   string       `json:"root_cause"`
	Severity    string       `json:"severity"` // "low" | "medium" | "high" | "critical"
	CanAutoHeal bool         `json:"can_auto_heal"`
	Actions     []LLMAction  `json:"actions"`
}

// LLMAction 建议执行的操作
type LLMAction struct {
	Type      string `json:"type"`      // "delete_pod"
	Target    string `json:"target"`    // Pod 名称
	Namespace string `json:"namespace"` // 所在命名空间
	Reason    string `json:"reason"`    // 执行理由
}

// analyzeFaults 将故障信息发给 LLM，返回结构化分析
func analyzeFaults(ctx context.Context, llm llmConfig, faults []PodFault) (*LLMAnalysis, error) {
	if llm.Provider == "" {
		return nil, nil // LLM 未启用
	}

	prompt := buildPrompt(faults)

	switch strings.ToLower(llm.Provider) {
	case "openai":
		return callOpenAI(ctx, llm, prompt)
	case "ollama":
		return callOllama(ctx, llm, prompt)
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", llm.Provider)
	}
}

// buildPrompt 构建发给 LLM 的提示词
func buildPrompt(faults []PodFault) string {
	var b strings.Builder
	b.WriteString(`You are a Kubernetes SRE assistant. Analyze the pod issues below and respond in JSON format.

Respond with this exact JSON structure (no markdown, no code fences):
{
  "analysis": "brief analysis of the issue",
  "root_cause": "identified root cause",
  "severity": "low|medium|high|critical",
  "can_auto_heal": true,
  "actions": [
    {
      "type": "delete_pod",
      "target": "pod-name",
      "namespace": "namespace",
      "reason": "why this action helps"
    }
  ]
}

Rules:
- severity: 'critical' if pod is down affecting service, 'high' if crash looping, 'medium' if image pull fail, 'low' if transient.
- can_auto_heal: true ONLY if deleting the pod would fix the issue (CrashLoopBackOff, OOMKill, etc.). false if it needs manual intervention (wrong image, missing secret).
- actions: list the concrete kubectl actions needed. For auto-heal, include "delete_pod" actions.
- Only suggest actions you are confident about. When unsure, set can_auto_heal=false and actions=[].

Current faults:
`)

	for i, f := range faults {
		b.WriteString(fmt.Sprintf("\n%d. Pod %s/%s phase=%s", i+1, f.Namespace, f.Name, f.Phase))
		for _, issue := range f.Issues {
			b.WriteString(fmt.Sprintf("\n   - container=%s ready=%v restarts=%d",
				issue.Container, issue.Ready, issue.RestartCount))
		}
	}

	b.WriteString(`

Respond with the JSON only.`)
	return b.String()
}

// ---------------------------------------------------------------------------
// OpenAI-compatible API 调用
// ---------------------------------------------------------------------------

func callOpenAI(ctx context.Context, cfg llmConfig, prompt string) (*LLMAnalysis, error) {
	url := strings.TrimRight(cfg.APIURL, "/") + "/chat/completions"

	body := openAIRequest{
		Model: cfg.Model,
		Messages: []openAIMessage{
			{Role: "system", Content: "You are a Kubernetes SRE assistant. Always respond in valid JSON."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.1,
		Stream:      false,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal openai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var chat openAIResponse
	if err := json.Unmarshal(respBody, &chat); err != nil {
		return nil, fmt.Errorf("parse openai response: %w (body: %s)", err, truncate(string(respBody), 200))
	}
	if chat.Error != nil {
		return nil, fmt.Errorf("openai API error: %s", chat.Error.Message)
	}
	if len(chat.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no choices")
	}

	return parseAnalysis(chat.Choices[0].Message.Content)
}

// ---------------------------------------------------------------------------
// Ollama API 调用
// ---------------------------------------------------------------------------

func callOllama(ctx context.Context, cfg llmConfig, prompt string) (*LLMAnalysis, error) {
	url := strings.TrimRight(cfg.APIURL, "/") + "/api/chat"

	body := ollamaRequest{
		Model: cfg.Model,
		Messages: []openAIMessage{
			{Role: "system", Content: "You are a Kubernetes SRE assistant. Always respond in valid JSON."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.1,
		Stream:      false,
		Format:      "json",
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var chat ollamaResponse
	if err := json.Unmarshal(respBody, &chat); err != nil {
		return nil, fmt.Errorf("parse ollama response: %w (body: %s)", err, truncate(string(respBody), 200))
	}
	if chat.Error != "" {
		return nil, fmt.Errorf("ollama error: %s", chat.Error)
	}

	return parseAnalysis(chat.Message.Content)
}

// ---------------------------------------------------------------------------
// 解析 LLM JSON 响应
// ---------------------------------------------------------------------------

func parseAnalysis(raw string) (*LLMAnalysis, error) {
	// 尝试提取 JSON（LLM 有时会在前后加多余文字）
	cleaned := raw
	if idx := strings.Index(raw, "{"); idx >= 0 {
		if end := strings.LastIndex(raw, "}"); end > idx {
			cleaned = raw[idx : end+1]
		}
	}

	var analysis LLMAnalysis
	if err := json.Unmarshal([]byte(cleaned), &analysis); err != nil {
		return nil, fmt.Errorf("parse LLM JSON: %w\nraw: %s", err, truncate(raw, 300))
	}

	// 兜底
	if analysis.Severity == "" {
		analysis.Severity = "medium"
	}
	if analysis.Actions == nil {
		analysis.Actions = []LLMAction{}
	}

	return &analysis, nil
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func loadLLMConfig() llmConfig {
	return llmConfig{
		Provider: osGetEnv("LLM_PROVIDER"),
		APIURL:   osGetEnv("LLM_API_URL"),
		APIKey:   osGetEnv("LLM_API_KEY"),
		Model:    osGetEnvDefault("LLM_MODEL", "gpt-4o-mini"),
	}
}

// osGetEnv 是 os.Getenv 的别名，方便测试（已经在 main.go 中通过 envOrDefault 使用）
// 这里使用 os.Getenv 直接调用
func osGetEnv(key string) string {
	return os.Getenv(key)
}

func osGetEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

