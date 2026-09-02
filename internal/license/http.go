package license

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

// HTTPProbe POSTs the key to the licensing API. It is unused until a key is entered.
type HTTPProbe struct {
	Client   *http.Client
	Endpoint string
}

func (h HTTPProbe) Check(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	endpoint := h.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	body, _ := json.Marshal(map[string]string{"edition": EditionCE})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w", ErrUnreachable)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w", ErrUnreachable)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w", ErrUnreachable)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%w", ErrUnreachable)
	}
	if !entitlementGranted(raw) {
		return fmt.Errorf("%w", ErrNotEntitled)
	}
	return nil
}

func entitlementGranted(raw []byte) bool {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	if b, ok := asBool(payload["accepted"]); ok && b {
		return true
	}
	if b, ok := asBool(payload["entitled"]); ok && b {
		return true
	}
	if sig, ok := payload["signature"].(string); ok && looksSigned(sig) {
		return true
	}
	if b, ok := asBool(payload["signed"]); ok && b {
		if sig, ok := payload["signature"].(string); ok && looksSigned(sig) {
			return true
		}
	}
	return false
}

func asBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func looksSigned(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) >= 16
}
