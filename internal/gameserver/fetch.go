package gameserver

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	maxFetchBytes  = 8 << 20
	maxBinaryBytes = 1500 << 20
)

// downloadGuard vets every artifact URL before it is fetched. Tests swap it
// to reach a loopback httptest server; production always uses
// validatePublicURL, which refuses private and loopback targets.
var downloadGuard = validatePublicURL

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

// digest is an expected checksum published by the upstream.
type digest struct {
	Algo string
	Hex  string
}

func newHasher(algo string) (hash.Hash, error) {
	switch strings.ToLower(algo) {
	case "sha256":
		return sha256.New(), nil
	case "sha1":
		return sha1.New(), nil
	case "sha512":
		return sha512.New(), nil
	case "md5":
		return md5.New(), nil
	}
	return nil, fmt.Errorf("checksum algorithm %s is not supported", algo)
}

func verifyFileDigest(path string, want digest) error {
	if strings.TrimSpace(want.Hex) == "" {
		return nil
	}
	h, err := newHasher(want.Algo)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(want.Hex)) {
		return fmt.Errorf("%s checksum mismatch: expected %s, got %s", want.Algo, want.Hex, got)
	}
	return nil
}

// fetchFileRetry downloads with resume and bounded retries, then verifies
// the checksum when the upstream published one. A checksum mismatch deletes
// the file and is not retried with the same bytes.
func fetchFileRetry(ctx context.Context, raw, dest string, maxBytes int64, want digest, attempts int) error {
	if attempts <= 0 {
		attempts = 1
	}
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(i*i*2) * time.Second):
			}
		}
		last = fetchFileResume(ctx, raw, dest, maxBytes)
		if last == nil {
			if err := verifyFileDigest(dest, want); err != nil {
				_ = os.Remove(dest)
				last = err
				continue
			}
			return nil
		}
		if strings.Contains(last.Error(), "HTTP 404") || strings.Contains(last.Error(), "HTTP 403") || strings.Contains(last.Error(), "HTTP 401") {
			return last
		}
	}
	return last
}

// fetchFileResume continues an interrupted .part download with a Range
// request when the server supports it and starts over otherwise.
func fetchFileResume(ctx context.Context, raw, dest string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = maxBinaryBytes
	}
	if err := downloadGuard(raw); err != nil {
		return err
	}
	tmp := dest + ".part"
	var offset int64
	if st, err := os.Stat(tmp); err == nil && st.Size() > 0 {
		offset = st.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "No-DAL-GameServers/1.0 (https://github.com/ASPEC-RACING-PTY-LTD/ndl-ce)")
	req.Header.Set("Accept", "*/*")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	res, err := downloadHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		_ = os.Remove(tmp)
		return fmt.Errorf("resume offset rejected; restarting")
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("remote source returned HTTP %d", res.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if res.StatusCode == http.StatusPartialContent && offset > 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	} else {
		offset = 0
	}
	f, err := os.OpenFile(tmp, flags, 0o640)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(res.Body, maxBytes-offset+1))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if offset+n > maxBytes {
		_ = os.Remove(tmp)
		return fmt.Errorf("remote file is larger than the allowed download size")
	}
	if offset+n == 0 {
		_ = os.Remove(tmp)
		return fmt.Errorf("remote file was empty")
	}
	return os.Rename(tmp, dest)
}

var hexDigestPattern = regexp.MustCompile(`\b([a-fA-F0-9]{64})\b`)

// fetchPublishedSHA256 reads a sha256sum-style file and returns the digest
// for name, or the only digest when the file covers a single artifact.
func fetchPublishedSHA256(ctx context.Context, raw, name string) (digest, error) {
	body, _, err := fetchURL(ctx, defaultHTTPClient(), raw)
	if err != nil {
		return digest{}, fmt.Errorf("could not read published checksum: %w", err)
	}
	return parseSHA256Sums(string(body), name)
}

// parseSHA256Sums reads sha256sum output (or a bare digest file).
func parseSHA256Sums(body, name string) (digest, error) {
	var only string
	count := 0
	for _, line := range strings.Split(body, "\n") {
		m := hexDigestPattern.FindStringSubmatch(line)
		if len(m) != 2 {
			continue
		}
		count++
		only = m[1]
		if name != "" && strings.Contains(line, name) {
			return digest{Algo: "sha256", Hex: m[1]}, nil
		}
	}
	if count == 1 {
		return digest{Algo: "sha256", Hex: only}, nil
	}
	return digest{}, fmt.Errorf("published checksum file has no entry for %s", name)
}
