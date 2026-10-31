// Package codeholddl finds CodeHold's published binaries (the ch CLI and the
// Actions runner) on codehold.dev. "latest" is resolved to one release
// folder, so a file and its SHA-256 always come from the same release.
package codeholddl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Public is the CodeHold website that publishes the binaries.
const Public = "https://codehold.dev"

// Release is one published release folder and its checksums by file name.
type Release struct {
	Dir  string
	Sums map[string]string
}

// File is the download URL and SHA-256 of one file in the release.
func (r Release) File(name string) (url, sha string, err error) {
	sha = r.Sums[name]
	if sha == "" {
		return "", "", fmt.Errorf("CodeHold release %s does not list %s", r.Dir, name)
	}
	return r.Dir + "/" + name, sha, nil
}

// CLIName and RunnerName are the Linux file names for a CPU type.
func CLIName(arch string) string    { return "ch-linux-" + arch }
func RunnerName(arch string) string { return "codehold-runner-linux-" + arch }

var (
	sumLine  = regexp.MustCompile(`^([0-9a-f]{64})\s+\*?([A-Za-z0-9._-]+)$`)
	mu       sync.Mutex
	cached   Release
	cachedAt time.Time
)

// Client is used for the lookup; tests replace it.
var Client = &http.Client{Timeout: 30 * time.Second}

// Latest is the current release on codehold.dev, looked up at most every
// ten minutes.
func Latest(ctx context.Context) (Release, error) {
	mu.Lock()
	defer mu.Unlock()
	if cached.Dir != "" && time.Since(cachedAt) < 10*time.Minute {
		return cached, nil
	}
	rel, err := fetch(ctx, Public+"/downloads/latest/SHA256SUMS")
	if err != nil {
		return Release{}, err
	}
	cached, cachedAt = rel, time.Now()
	return rel, nil
}

func fetch(ctx context.Context, raw string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return Release{}, err
	}
	res, err := Client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("could not reach codehold.dev: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("codehold.dev returned HTTP %d for the release checksums", res.StatusCode)
	}
	final := res.Request.URL
	if final.Scheme != "https" || !strings.HasSuffix(final.Path, "/SHA256SUMS") {
		return Release{}, fmt.Errorf("codehold.dev sent the checksums from an unexpected address")
	}
	final.Path = strings.TrimSuffix(final.Path, "/SHA256SUMS")
	final.RawQuery, final.Fragment = "", ""
	rel := Release{Dir: final.String(), Sums: map[string]string{}}
	sc := bufio.NewScanner(io.LimitReader(res.Body, 64<<10))
	for sc.Scan() {
		if m := sumLine.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
			rel.Sums[m[2]] = m[1]
		}
	}
	if len(rel.Sums) == 0 {
		return Release{}, fmt.Errorf("the codehold.dev release lists no checksums")
	}
	return rel, nil
}

// Set makes rel the current release without a lookup; tests use it.
func Set(rel Release) {
	mu.Lock()
	cached, cachedAt = rel, time.Now()
	mu.Unlock()
}
