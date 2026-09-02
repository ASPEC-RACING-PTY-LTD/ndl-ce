package ndltls

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ChallengeMem serves HTTP-01 tokens for ACME.
type ChallengeMem struct {
	mu   sync.Mutex
	toks map[string]string
}

func (c *ChallengeMem) Put(token, keyAuth string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.toks == nil {
		c.toks = map[string]string{}
	}
	c.toks[token] = keyAuth
}

func (c *ChallengeMem) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")
	c.mu.Lock()
	val, ok := c.toks[token]
	c.mu.Unlock()
	if !ok || token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, val)
}

// ProbeDirectory GETs an ACME directory. It does not issue a certificate.
func ProbeDirectory(ctx context.Context, directory string) error {
	u, err := url.Parse(strings.TrimSpace(directory))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("acme directory must be an https URL")
	}
	if u.User != nil {
		return fmt.Errorf("acme directory must not include credentials")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("acme directory is unavailable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("acme directory returned HTTP %d", res.StatusCode)
	}
	var dir map[string]any
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&dir); err != nil {
		return fmt.Errorf("acme directory is not JSON")
	}
	if dir["newNonce"] == nil && dir["newAccount"] == nil && dir["newOrder"] == nil {
		return fmt.Errorf("acme directory is missing required fields")
	}
	return nil
}
