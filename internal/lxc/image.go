package lxc

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed lxc-images.asc
var lxcImageKey []byte

// HTTPDoer is an injectable client. Tests fake it. Production uses http.Client.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type streamsFile struct {
	Products map[string]streamsProduct `json:"products"`
}

type streamsProduct struct {
	Versions map[string]streamsVersion `json:"versions"`
}

type streamsVersion struct {
	Items map[string]streamsItem `json:"items"`
}

type streamsItem struct {
	Ftype  string `json:"ftype"`
	SHA256 string `json:"sha256"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
}

type imageArtifact struct {
	SHA256 string
	Path   string
}

func (e *Engine) http() HTTPDoer {
	if e.HTTP != nil {
		return e.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (e *Engine) imageBase() string {
	if e.ImageBase != "" {
		return strings.TrimRight(e.ImageBase, "/")
	}
	return DefaultImageBase
}

func (e *Engine) fetchAndUnpack(ctx context.Context, pin, rootfs string) (verified bool, sha string, err error) {
	if err := os.MkdirAll(rootfs, 0o750); err != nil {
		return false, "", err
	}
	if e.SkipHostCmds && e.HTTP == nil {
		return false, "", writeRootfsMarker(rootfs)
	}
	art, err := e.resolveImage(ctx, pin)
	if err != nil {
		return false, "", err
	}
	archive, err := e.ensureCached(ctx, pin, art)
	if err != nil {
		return false, "", err
	}
	if e.FakeUnpack || e.SkipHostCmds {
		if err := writeRootfsMarker(rootfs); err != nil {
			return false, "", err
		}
		return true, art.SHA256, nil
	}
	if err := e.verifyGPG(ctx, archive, art.Path); err != nil {
		return false, "", err
	}
	if err := e.unpackTar(ctx, archive, rootfs); err != nil {
		return false, "", err
	}
	if err := writeRootfsMarker(rootfs); err != nil {
		return false, "", err
	}
	return true, art.SHA256, nil
}

func (e *Engine) resolveImage(ctx context.Context, pin string) (imageArtifact, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.imageBase()+imageIndexPath, nil)
	if err != nil {
		return imageArtifact{}, err
	}
	res, err := e.http().Do(req)
	if err != nil {
		return imageArtifact{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return imageArtifact{}, fmt.Errorf("image index HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return imageArtifact{}, err
	}
	var idx streamsFile
	if err := json.Unmarshal(body, &idx); err != nil {
		return imageArtifact{}, fmt.Errorf("image index: %w", err)
	}
	prod, ok := idx.Products[ProductKey(pin)]
	if !ok {
		return imageArtifact{}, fmt.Errorf("image pin %q is not in simplestreams", pin)
	}
	keys := make([]string, 0, len(prod.Versions))
	for k := range prod.Versions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return imageArtifact{}, fmt.Errorf("image pin %q has no versions", pin)
	}
	ver := prod.Versions[keys[len(keys)-1]]
	item, ok := pickRootfs(ver.Items)
	if !ok {
		return imageArtifact{}, fmt.Errorf("image pin %q has no rootfs tarball", pin)
	}
	if item.SHA256 == "" || item.Path == "" {
		return imageArtifact{}, fmt.Errorf("image pin %q is missing sha256 or path", pin)
	}
	return imageArtifact{SHA256: strings.ToLower(item.SHA256), Path: item.Path}, nil
}

func pickRootfs(items map[string]streamsItem) (streamsItem, bool) {
	for _, key := range []string{"root.tar.xz", "rootfs.tar.xz", "root.tar.gz"} {
		if item, ok := items[key]; ok {
			return item, true
		}
	}
	for _, item := range items {
		ft := strings.ToLower(item.Ftype)
		if ft == "root.tar.xz" || ft == "rootfs.tar.xz" || strings.Contains(ft, "root.tar") {
			return item, true
		}
	}
	return streamsItem{}, false
}

func (e *Engine) ensureCached(ctx context.Context, pin string, art imageArtifact) (string, error) {
	cache := filepath.Join(e.cacheDir(), filepath.FromSlash(pin))
	if err := os.MkdirAll(cache, 0o750); err != nil {
		return "", err
	}
	dest := filepath.Join(cache, art.SHA256+".tar.xz")
	if b, err := os.ReadFile(dest); err == nil {
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) == art.SHA256 {
			return dest, nil
		}
	}
	url := e.imageBase() + "/" + strings.TrimPrefix(art.Path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := e.http().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image tarball HTTP %d", res.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, 2<<30)); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != art.SHA256 {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("image sha256 mismatch: got %s want %s", got, art.SHA256)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dest, nil
}

func (e *Engine) verifyGPG(ctx context.Context, archive, imagePath string) error {
	sigURL := e.imageBase() + "/" + strings.TrimPrefix(imagePath, "/") + ".asc"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sigURL, nil)
	if err != nil {
		return err
	}
	res, err := e.http().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("image signature HTTP %d", res.StatusCode)
	}
	sig, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	dir := filepath.Dir(archive)
	asc := archive + ".asc"
	if err := os.WriteFile(asc, sig, 0o640); err != nil {
		return err
	}
	keyring := filepath.Join(dir, "lxc-images.gpg")
	ring, err := dearmorPublicKey(lxcImageKey)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyring, ring, 0o640); err != nil {
		return err
	}
	if _, err := e.run(ctx, BinGPGV, "--keyring", keyring, asc, archive); err != nil {
		return fmt.Errorf("image gpg verify failed: %w", err)
	}
	return nil
}

func dearmorPublicKey(armor []byte) ([]byte, error) {
	const begin = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
	const end = "-----END PGP PUBLIC KEY BLOCK-----"
	s := string(armor)
	i := strings.Index(s, begin)
	j := strings.Index(s, end)
	if i < 0 || j < 0 || j <= i {
		return nil, fmt.Errorf("embedded LXC image key is not an armored public key")
	}
	var b64 strings.Builder
	for _, line := range strings.Split(s[i+len(begin):j], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, ":") || strings.HasPrefix(line, "=") {
			continue
		}
		b64.WriteString(line)
	}
	out, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil {
		return nil, fmt.Errorf("embedded LXC image key: %w", err)
	}
	if len(out) < 64 {
		return nil, fmt.Errorf("embedded LXC image key is too short")
	}
	return out, nil
}

func (e *Engine) unpackTar(ctx context.Context, archive, rootfs string) error {
	args := []string{"-x", "-C", rootfs, "-f", archive}
	if strings.HasSuffix(archive, ".tar.xz") || strings.HasSuffix(archive, ".xz") {
		args = []string{"-xJ", "-C", rootfs, "-f", archive}
	} else if strings.HasSuffix(archive, ".tar.gz") || strings.HasSuffix(archive, ".tgz") {
		args = []string{"-xz", "-C", rootfs, "-f", archive}
	}
	_, err := e.run(ctx, BinTar, args...)
	return err
}

func writeRootfsMarker(rootfs string) error {
	return os.WriteFile(filepath.Join(rootfs, RootfsMarker), []byte("ok\n"), 0o640)
}
