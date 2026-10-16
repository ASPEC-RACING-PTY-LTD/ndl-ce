// Package codehold speaks CodeHold's host controller protocol (see
// docs/runner-controller.md): enroll once with a one-time token, then sync in
// a loop, reporting what runs here and receiving the runners CodeHold wants.
package codehold

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Limits are a runner's per-job limits and how many jobs it runs at a time.
type Limits struct {
	Concurrency int     `json:"concurrency"`
	CPUs        float64 `json:"cpus"`
	MemoryMB    int     `json:"memory_mb"`
	Isolation   string  `json:"isolation"`
	Image       string  `json:"image"`
}

// DesiredRunner is one runner as CodeHold wants it.
type DesiredRunner struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	State             string `json:"state"`
	Limits            Limits `json:"limits"`
	Busy              int    `json:"busy"`
	Spec              string `json:"spec"`
	RegistrationToken string `json:"registration_token,omitempty"`
}

// CredentialOffer is a replacement controller credential.
type CredentialOffer struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SyncReply is CodeHold's answer to a sync.
type SyncReply struct {
	Runners    []DesiredRunner  `json:"runners"`
	Credential *CredentialOffer `json:"credential"`
}

// ObservedRunner is what this controller reports about one runner.
type ObservedRunner struct {
	ID         string   `json:"id"`
	State      string   `json:"state"`
	Error      string   `json:"error,omitempty"`
	Project    string   `json:"project,omitempty"`
	AuthFailed bool     `json:"auth_failed,omitempty"`
	Logs       []string `json:"logs,omitempty"`
}

// SyncRequest is one sync.
type SyncRequest struct {
	Wait      bool             `json:"wait"`
	Version   string           `json:"version"`
	Hostname  string           `json:"hostname"`
	OS        string           `json:"os"`
	Arch      string           `json:"arch"`
	Engine    string           `json:"engine"`
	CPUs      float64          `json:"cpus"`
	MemoryMB  int              `json:"memory_mb"`
	Image     string           `json:"image"`
	Network   string           `json:"network"`
	RunnerURL string           `json:"runner_url"`
	Error     string           `json:"error"`
	Logs      []string         `json:"logs"`
	Runners   []ObservedRunner `json:"runners"`
}

// Enrolled is the result of enrolling.
type Enrolled struct {
	ControllerID string `json:"controller_id"`
	Token        string `json:"controller_token"`
	Name         string `json:"name"`
}

// APIError is a non-2xx reply.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("CodeHold answered HTTP %d", e.Status)
	}
	return fmt.Sprintf("CodeHold answered HTTP %d: %s", e.Status, e.Message)
}

// Unauthorized reports whether CodeHold rejected the controller credential
// (as opposed to Cloudflare Access refusing the request).
func Unauthorized(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == http.StatusUnauthorized && api.Code != "cloudflare_access"
}

// Client calls CodeHold. Token is the controller credential.
type Client struct {
	URL      string
	Token    string
	CFID     string
	CFSecret string
	HTTP     *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{
		Timeout: 60 * time.Second,
		// A redirect means something in front of CodeHold (Cloudflare
		// Access, a login proxy) intercepted the request.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// CleanURL checks a CodeHold address.
func CleanURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("enter CodeHold's address, for example https://git.example.com")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.CFID != "" && c.CFSecret != "" {
		req.Header.Set("CF-Access-Client-Id", c.CFID)
		req.Header.Set("CF-Access-Client-Secret", c.CFSecret)
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("CodeHold could not be reached: %w", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return &APIError{Status: res.StatusCode, Code: "redirect", Message: "something in front of CodeHold redirected the request (Cloudflare Access or a login page?)"}
	}
	if res.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(data, &e)
		return &APIError{Status: res.StatusCode, Code: e.Code, Message: e.Error}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("CodeHold's reply was not understood: %w", err)
	}
	return nil
}

// Enroll exchanges a one-time enrollment token for a controller credential.
func (c *Client) Enroll(ctx context.Context, token, name, hostname, version string) (Enrolled, error) {
	var out Enrolled
	err := c.post(ctx, "/api/v1/actions/controller/enroll", map[string]string{
		"token": token, "name": name, "hostname": hostname, "version": version, "os": "linux", "arch": "amd64",
	}, &out)
	if err == nil && (out.ControllerID == "" || out.Token == "") {
		err = errors.New("CodeHold did not return a controller credential")
	}
	return out, err
}

// Sync reports and receives the desired runners. With Wait set, CodeHold
// holds the request for up to 25 seconds until something changes.
func (c *Client) Sync(ctx context.Context, req SyncRequest) (SyncReply, error) {
	var out SyncReply
	if req.Runners == nil {
		req.Runners = []ObservedRunner{}
	}
	if req.Logs == nil {
		req.Logs = []string{}
	}
	err := c.post(ctx, "/api/v1/actions/controller/sync", req, &out)
	return out, err
}
