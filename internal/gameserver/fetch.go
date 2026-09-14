package gameserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxFetchBytes  = 8 << 20
	maxBinaryBytes = 1500 << 20
)

func defaultHTTPClient() *http.Client {
	return httpClientWithTimeout(30 * time.Second)
}

func downloadHTTPClient() *http.Client {
	return httpClientWithTimeout(15 * time.Minute)
}

func httpClientWithTimeout(d time.Duration) *http.Client {
	return &http.Client{
		Timeout: d,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return validatePublicURL(req.URL.String())
		},
	}
}

func fetchFile(ctx context.Context, raw, dest string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = maxBinaryBytes
	}
	if err := validatePublicURL(raw); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "No-DAL-GameServers/1.0 (https://github.com/ASPEC-RACING-PTY-LTD/ndl-ce)")
	req.Header.Set("Accept", "*/*")
	res, err := downloadHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return fmt.Errorf("remote source returned HTTP %d", res.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(res.Body, maxBytes+1))
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if n > maxBytes {
		_ = os.Remove(tmp)
		return fmt.Errorf("remote file is larger than the allowed download size")
	}
	if n == 0 {
		_ = os.Remove(tmp)
		return fmt.Errorf("remote file was empty")
	}
	return os.Rename(tmp, dest)
}

func fetchBinary(ctx context.Context, raw string, maxBytes int64) ([]byte, error) {
	dir, err := os.MkdirTemp("", "ndl-gs-dl-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	dest := filepath.Join(dir, "blob")
	if err := fetchFile(ctx, raw, dest, maxBytes); err != nil {
		return nil, err
	}
	return os.ReadFile(dest)
}

func validatePublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL is not valid")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("only http and https catalogue URLs are allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL host is required")
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") {
		return fmt.Errorf("catalogue URL must not target localhost")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		// Allow DNS failures at validation time for offline tests; fetch will fail later.
		if isBlockedHost(host) {
			return fmt.Errorf("catalogue URL host is not allowed")
		}
		return nil
	}
	if len(ips) == 0 {
		return fmt.Errorf("catalogue URL host has no addresses")
	}
	for _, ip := range ips {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("catalogue URL must not target a private or link-local address")
		}
	}
	return nil
}

func isBlockedHost(host string) bool {
	h := strings.ToLower(host)
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "0.0.0.0" || strings.HasSuffix(h, ".internal")
}

func fetchURL(ctx context.Context, client *http.Client, raw string) ([]byte, string, error) {
	if err := validatePublicURL(raw); err != nil {
		return nil, "", err
	}
	if client == nil {
		client = defaultHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "No-DAL-GameServers/1.0 (https://github.com/ASPEC-RACING-PTY-LTD/ndl-ce)")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	res, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxFetchBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxFetchBytes {
		return nil, "", fmt.Errorf("remote file is larger than 8 MiB")
	}
	if res.StatusCode >= 400 {
		return nil, "", fmt.Errorf("remote source returned HTTP %d", res.StatusCode)
	}
	ct := res.Header.Get("Content-Type")
	return body, ct, nil
}
