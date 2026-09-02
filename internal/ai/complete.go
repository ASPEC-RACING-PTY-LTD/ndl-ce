package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CompleteRequest is a provider-neutral completion. It is not a shell.
type CompleteRequest struct {
	Kind     string
	Endpoint string
	Model    string
	APIKey   string
	Prompt   string
}

// Completer talks to a BYO model. Local kind must not call the network.
type Completer interface {
	Complete(ctx context.Context, req CompleteRequest) (string, error)
}

// HTTPCompleter posts OpenAI-compatible chat completions. Other kinds use the same shape when the endpoint is set.
type HTTPCompleter struct {
	Client *http.Client
}

func (h HTTPCompleter) Complete(ctx context.Context, req CompleteRequest) (string, error) {
	if req.Kind == KindLocal || strings.TrimSpace(req.Endpoint) == "" {
		return "", fmt.Errorf("provider endpoint is not configured")
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{
		"model":    firstNonEmpty(req.Model, "local"),
		"messages": []map[string]string{{"role": "user", "content": req.Prompt}},
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("provider request is invalid")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	}
	res, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("provider unavailable")
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("provider unavailable")
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("provider response is unusable")
	}
	return Redact(parsed.Choices[0].Message.Content), nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
