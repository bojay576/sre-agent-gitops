package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultListenAddr = ":3000"

type config struct {
	Provider string
	APIURL   string
	APIKey   string
	Model    string
	MCPURL   string
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model,omitempty"`
	Message  string        `json:"message,omitempty"`
	Messages []chatMessage `json:"messages,omitempty"`
	Stream   bool          `json:"stream,omitempty"`
}

type chatResponse struct {
	Model     string      `json:"model"`
	Provider  string      `json:"provider"`
	Message   chatMessage `json:"message"`
	CreatedAt time.Time   `json:"created_at"`
	MCPServer string      `json:"mcp_server,omitempty"`
}

type openAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int         `json:"index"`
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

func main() {
	cfg := loadConfig()
	client := &http.Client{Timeout: 120 * time.Second}

	mux := http.NewServeMux()
	mux.HandleFunc("/", statusHandler(cfg))
	mux.HandleFunc("/healthz", statusHandler(cfg))
	mux.HandleFunc("/chat", chatHandler(cfg, client, false))
	mux.HandleFunc("/v1/chat/completions", chatHandler(cfg, client, true))

	addr := envOrDefault("LISTEN_ADDR", defaultListenAddr)
	log.Printf("AI Gateway listening on %s with provider=%s model=%s api_url=%s", addr, cfg.Provider, cfg.Model, cfg.APIURL)
	if err := http.ListenAndServe(addr, withRequestLogging(mux)); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() config {
	apiURL := envOrDefault("LLM_API_URL", os.Getenv("OLLAMA_URL"))
	if apiURL == "" {
		apiURL = "http://ollama-service:11434/api/chat"
	}

	provider := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	if provider == "" {
		provider = inferProvider(apiURL)
	}

	return config{
		Provider: provider,
		APIURL:   apiURL,
		APIKey:   os.Getenv("LLM_API_KEY"),
		Model:    envOrDefault("LLM_MODEL", envOrDefault("OLLAMA_MODEL", "qwen3:4b")),
		MCPURL:   envOrDefault("MCP_SERVER_URL", "http://mcp-server-service:8080/sse"),
	}
}

func inferProvider(apiURL string) string {
	if strings.Contains(apiURL, "/v1/chat/completions") || strings.Contains(apiURL, "openai") || strings.Contains(apiURL, "dashscope") || strings.Contains(apiURL, "deepseek") {
		return "openai"
	}
	return "ollama"
}

func statusHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/healthz" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status":     "ok",
			"provider":   cfg.Provider,
			"model":      cfg.Model,
			"mcp_server": cfg.MCPURL,
		})
	}
}

func chatHandler(cfg config, client *http.Client, openAICompatible bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		var req chatRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON request")
			return
		}
		if req.Model == "" {
			req.Model = cfg.Model
		}
		messages, err := normalizeMessages(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()

		answer, err := callLLM(ctx, client, cfg, req.Model, messages)
		if err != nil {
			log.Printf("LLM request failed: %v", err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}

		msg := chatMessage{Role: "assistant", Content: answer}
		if openAICompatible {
			writeJSON(w, http.StatusOK, makeOpenAIResponse(req.Model, msg))
			return
		}
		writeJSON(w, http.StatusOK, chatResponse{
			Model:     req.Model,
			Provider:  cfg.Provider,
			Message:   msg,
			CreatedAt: time.Now().UTC(),
			MCPServer: cfg.MCPURL,
		})
	}
}

func normalizeMessages(req chatRequest) ([]chatMessage, error) {
	if len(req.Messages) == 0 && strings.TrimSpace(req.Message) != "" {
		req.Messages = []chatMessage{{Role: "user", Content: req.Message}}
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("message or messages is required")
	}
	for i := range req.Messages {
		req.Messages[i].Role = strings.TrimSpace(req.Messages[i].Role)
		req.Messages[i].Content = strings.TrimSpace(req.Messages[i].Content)
		if req.Messages[i].Role == "" {
			req.Messages[i].Role = "user"
		}
		if req.Messages[i].Content == "" {
			return nil, errors.New("message content cannot be empty")
		}
	}
	return req.Messages, nil
}

func callLLM(ctx context.Context, client *http.Client, cfg config, model string, messages []chatMessage) (string, error) {
	switch cfg.Provider {
	case "openai", "custom":
		return callOpenAI(ctx, client, cfg, model, messages)
	case "ollama", "":
		return callOllama(ctx, client, cfg, model, messages)
	default:
		return "", fmt.Errorf("unsupported LLM_PROVIDER %q", cfg.Provider)
	}
}

func callOllama(ctx context.Context, client *http.Client, cfg config, model string, messages []chatMessage) (string, error) {
	payload := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   false,
	}

	var decoded struct {
		Message  chatMessage `json:"message"`
		Response string      `json:"response"`
		Error    string      `json:"error"`
	}
	if err := postJSON(ctx, client, cfg, payload, &decoded); err != nil {
		return "", err
	}
	if decoded.Error != "" {
		return "", errors.New(decoded.Error)
	}
	if decoded.Message.Content != "" {
		return decoded.Message.Content, nil
	}
	return decoded.Response, nil
}

func callOpenAI(ctx context.Context, client *http.Client, cfg config, model string, messages []chatMessage) (string, error) {
	payload := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   false,
	}

	var decoded struct {
		Choices []struct {
			Message chatMessage `json:"message"`
			Text    string      `json:"text"`
		} `json:"choices"`
		Error interface{} `json:"error"`
	}
	if err := postJSON(ctx, client, cfg, payload, &decoded); err != nil {
		return "", err
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("provider returned error: %v", decoded.Error)
	}
	if len(decoded.Choices) == 0 {
		return "", errors.New("provider returned no choices")
	}
	if decoded.Choices[0].Message.Content != "" {
		return decoded.Choices[0].Message.Content, nil
	}
	return decoded.Choices[0].Text, nil
}

func postJSON(ctx context.Context, client *http.Client, cfg config, payload interface{}, target interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("invalid provider response: %w", err)
	}
	return nil
}

func makeOpenAIResponse(model string, message chatMessage) openAIResponse {
	resp := openAIResponse{
		ID:      "chatcmpl-" + randomHex(12),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
	}
	resp.Choices = append(resp.Choices, struct {
		Index        int         `json:"index"`
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	}{Index: 0, Message: message, FinishReason: "stop"})
	return resp
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response failed: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
