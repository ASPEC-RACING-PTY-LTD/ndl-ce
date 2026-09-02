package license

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	body, _ := json.Marshal(map[string]string{"key": key})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("licensing request is invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("licensing API unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("licensing API unreachable")
	}
	return nil
}
