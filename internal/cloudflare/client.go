// Package cloudflare talks to the Cloudflare v4 REST API for tunnels, their
// remote configuration, zones and DNS records. It uses scoped API tokens
// only (Authorization: Bearer); global API keys are not supported.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the Cloudflare v4 API root.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// Error classes callers branch on. Use errors.Is.
var (
	// ErrAuth means the token is invalid, expired or disabled.
	ErrAuth = errors.New("cloudflare rejected the API token")
	// ErrPermission means the token is valid but lacks a permission for this call.
	ErrPermission = errors.New("the API token lacks permission for this action")
	// ErrNotFound means the resource does not exist or the token cannot see it.
	ErrNotFound = errors.New("not found in Cloudflare")
	// ErrRateLimited means Cloudflare is throttling this token or user.
	ErrRateLimited = errors.New("cloudflare rate limit reached")
	// ErrUnavailable means Cloudflare or the network failed; retry later.
	ErrUnavailable = errors.New("cloudflare is unavailable")
	// ErrConflict means the request clashed with existing state.
	ErrConflict = errors.New("cloudflare reported a conflict")
)

// APIError is a failed Cloudflare call. It wraps one of the Err* classes.
type APIError struct {
	Status     int
	Codes      []int
	Message    string
	RetryAfter time.Duration
	class      error
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.class.Error()
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s (retry in %s)", msg, e.RetryAfter.Round(time.Second))
	}
	return msg
}

func (e *APIError) Unwrap() error { return e.class }

// Client calls the API with one token. It is safe for concurrent use.
type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
	// MinGap spaces requests made with this token; 0 uses DefaultMinGap.
	MinGap time.Duration

	mu   sync.Mutex
	next time.Time
}

// DefaultMinGap keeps one token well under Cloudflare's 1200 requests per
// five minutes (four per second) even when several pages refresh at once.
const DefaultMinGap = 300 * time.Millisecond

type envelope struct {
	Success  bool            `json:"success"`
	Errors   []apiMessage    `json:"errors"`
	Result   json.RawMessage `json:"result"`
	Info     *resultInfo     `json:"result_info"`
	Messages []apiMessage    `json:"messages"`
}

type apiMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type resultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// pace waits for this token's turn so bursts stay under the API limit.
func (c *Client) pace(ctx context.Context) error {
	gap := c.MinGap
	if gap == 0 {
		gap = DefaultMinGap
	}
	if gap < 0 {
		return nil
	}
	c.mu.Lock()
	now := time.Now()
	at := c.next
	if at.Before(now) {
		at = now
	}
	c.next = at.Add(gap)
	c.mu.Unlock()
	if wait := time.Until(at); wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// do runs one request and decodes result into out. It never logs the token.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) (*resultInfo, error) {
	if strings.TrimSpace(c.Token) == "" {
		return nil, &APIError{Message: "no API token is configured", class: ErrAuth}
	}
	if err := c.pace(ctx); err != nil {
		return nil, err
	}
	u := c.base() + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &APIError{Message: "could not reach Cloudflare: " + redactErr(err), class: ErrUnavailable}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	var env envelope
	_ = json.Unmarshal(raw, &env)
	if res.StatusCode >= 200 && res.StatusCode < 300 && (env.Success || len(env.Errors) == 0) {
		if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
			if err := json.Unmarshal(env.Result, out); err != nil {
				return nil, &APIError{Status: res.StatusCode, Message: "unexpected Cloudflare response", class: ErrUnavailable}
			}
		}
		return env.Info, nil
	}
	return nil, classify(res, env)
}

func classify(res *http.Response, env envelope) *APIError {
	e := &APIError{Status: res.StatusCode}
	var msgs []string
	for _, m := range env.Errors {
		e.Codes = append(e.Codes, m.Code)
		if m.Message != "" {
			msgs = append(msgs, m.Message)
		}
	}
	e.Message = strings.Join(msgs, "; ")
	has := func(codes ...int) bool {
		for _, c := range e.Codes {
			for _, want := range codes {
				if c == want {
					return true
				}
			}
		}
		return false
	}
	switch {
	case res.StatusCode == http.StatusTooManyRequests || has(971, 10429):
		e.class = ErrRateLimited
		e.RetryAfter = retryAfter(res.Header.Get("Retry-After"))
		if e.RetryAfter == 0 {
			e.RetryAfter = 5 * time.Minute
		}
		if e.Message == "" {
			e.Message = "Cloudflare rate limit reached"
		}
	case res.StatusCode == http.StatusUnauthorized || has(1000, 9106, 9109, 10000) && res.StatusCode != http.StatusForbidden:
		e.class = ErrAuth
		if e.Message == "" {
			e.Message = "Cloudflare rejected the API token; it may be expired or revoked"
		}
	case res.StatusCode == http.StatusForbidden:
		e.class = ErrPermission
		if e.Message == "" {
			e.Message = "the API token lacks permission for this action"
		}
	case res.StatusCode == http.StatusNotFound:
		e.class = ErrNotFound
	case res.StatusCode == http.StatusConflict || has(81053, 81057, 81058, 1003):
		e.class = ErrConflict
	case res.StatusCode >= 500:
		e.class = ErrUnavailable
		if e.Message == "" {
			e.Message = "Cloudflare returned " + strconv.Itoa(res.StatusCode) + "; try again shortly"
		}
	default:
		e.class = ErrConflict
		if e.Message == "" {
			e.Message = "Cloudflare refused the request (" + strconv.Itoa(res.StatusCode) + ")"
		}
	}
	return e
}

func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// redactErr strips the request URL query from transport errors.
func redactErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// paged fetches every page of a list endpoint.
func paged[T any](ctx context.Context, c *Client, path string, query url.Values, perPage int) ([]T, error) {
	var all []T
	if query == nil {
		query = url.Values{}
	}
	for page := 1; page <= 200; page++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(perPage))
		var batch []T
		info, err := c.do(ctx, http.MethodGet, path, q, nil, &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if info == nil || len(batch) < perPage {
			break
		}
		if info.TotalPages > 0 && page >= info.TotalPages {
			break
		}
		if info.TotalCount > 0 && len(all) >= info.TotalCount {
			break
		}
	}
	return all, nil
}
