// Package liveupdate installs signed NDL-CE updates without a package
// rebuild: a new web UI is switched in place, and new control-plane or agent
// binaries are swapped atomically, restarted, health-checked and rolled back
// when they do not come up.
//
// A bundle is a gzip tar holding manifest.json, manifest.sig and files/…
// The signature covers the exact manifest bytes, and the manifest pins the
// SHA-256 of every file, so a bundle is accepted only whole and only when a
// trusted key signed it.
package liveupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Component names.
const (
	CompUI      = "ui"
	CompControl = "control"
	CompAgent   = "agent"
)

// Binary paths inside a bundle.
const (
	binControl = "bin/ndl-control"
	binAgent   = "bin/ndl-agent"
)

// Limits keep a hostile bundle from filling the disk or memory.
const (
	maxBundleBytes = 512 << 20
	maxFiles       = 20000
	maxMetaBytes   = 4 << 20
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// File is one file a bundle installs.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest describes a live update. It is signed as the exact bytes stored.
type Manifest struct {
	ID string `json:"id"`
	// Version is shown to people, for example 1.5.0+live.3.
	Version string `json:"version"`
	// BaseVersion is the package version the bundle was built from. It is
	// applied only on a host running the same major.minor.
	BaseVersion string    `json:"base_version"`
	CreatedAt   time.Time `json:"created_at"`
	Notes       string    `json:"notes,omitempty"`
	Files       []File    `json:"files"`
	// Migrations are the database migrations the control binary carries.
	Migrations []string `json:"migrations,omitempty"`
	// AllowMigrations lets the control binary apply migrations the
	// database does not have yet. Off by default: a rollback cannot undo
	// a schema change.
	AllowMigrations bool `json:"allow_migrations,omitempty"`
}

// Components lists what the bundle replaces, in apply order.
func (m Manifest) Components() []string {
	var ui, control, agent bool
	for _, f := range m.Files {
		switch {
		case f.Path == binAgent:
			agent = true
		case f.Path == binControl:
			control = true
		case strings.HasPrefix(f.Path, "ui/"):
			ui = true
		}
	}
	var out []string
	if agent {
		out = append(out, CompAgent)
	}
	if control {
		out = append(out, CompControl)
	}
	if ui {
		out = append(out, CompUI)
	}
	return out
}

func allowedPath(p string) bool {
	if p != path.Clean(p) || strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.Contains(p, "\\") {
		return false
	}
	return p == binControl || p == binAgent || (strings.HasPrefix(p, "ui/") && len(p) > 3)
}

func (m Manifest) validate() error {
	if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("bundle id %q is not valid", m.ID)
	}
	if strings.TrimSpace(m.Version) == "" || strings.TrimSpace(m.BaseVersion) == "" {
		return errors.New("bundle has no version")
	}
	if len(m.Files) == 0 || len(m.Files) > maxFiles {
		return errors.New("bundle file list is empty or too long")
	}
	seen := map[string]bool{}
	var total int64
	for _, f := range m.Files {
		if !allowedPath(f.Path) {
			return fmt.Errorf("bundle may not install %q", f.Path)
		}
		if seen[f.Path] {
			return fmt.Errorf("bundle lists %q twice", f.Path)
		}
		seen[f.Path] = true
		if len(f.SHA256) != 64 || f.Size < 0 {
			return fmt.Errorf("bundle entry %q has no valid checksum", f.Path)
		}
		total += f.Size
	}
	if total > maxBundleBytes {
		return errors.New("bundle is too large")
	}
	if !seen["ui/index.html"] && hasPrefix(m.Files, "ui/") {
		return errors.New("a UI update must contain ui/index.html")
	}
	return nil
}

func hasPrefix(files []File, prefix string) bool {
	for _, f := range files {
		if strings.HasPrefix(f.Path, prefix) {
			return true
		}
	}
	return false
}

// Signature is manifest.sig.
type Signature struct {
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

// KeyID names a public key: the first 16 hex digits of its SHA-256.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// Keys are the public keys a host trusts, by key ID.
type Keys map[string]ed25519.PublicKey

// LoadKeys reads every *.pub file in dir: one base64 Ed25519 public key.
func LoadKeys(dir string) (Keys, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Keys{}, nil
		}
		return nil, err
	}
	keys := Keys{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".pub" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		pub, err := ParsePublicKey(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		keys[KeyID(pub)] = pub
	}
	return keys, nil
}

// ParsePublicKey decodes a base64 Ed25519 public key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("not an Ed25519 public key")
	}
	return ed25519.PublicKey(b), nil
}

// ParsePrivateKey decodes a base64 Ed25519 private key (seed or full key).
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("not an Ed25519 private key")
	}
	switch len(b) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(b), nil
	}
	return nil, errors.New("not an Ed25519 private key")
}

// Pack writes a signed bundle of the files under src (relative paths as
// the bundle installs them, for example ui/index.html or bin/ndl-agent).
func Pack(w io.Writer, m Manifest, src string, key ed25519.PrivateKey) (Manifest, error) {
	m.Files = nil
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		sum, size, err := hashFile(p)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, File{Path: rel, SHA256: sum, Size: size})
		return nil
	})
	if err != nil {
		return m, err
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	if err := m.validate(); err != nil {
		return m, err
	}
	manRaw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	sigRaw, _ := json.Marshal(Signature{
		KeyID:     KeyID(key.Public().(ed25519.PublicKey)),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, manRaw)),
	})
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	put := func(name string, mode int64, size int64, r io.Reader) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: size, ModTime: m.CreatedAt, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := io.Copy(tw, r)
		return err
	}
	if err := put("manifest.json", 0o644, int64(len(manRaw)), bytes.NewReader(manRaw)); err != nil {
		return m, err
	}
	if err := put("manifest.sig", 0o644, int64(len(sigRaw)), bytes.NewReader(sigRaw)); err != nil {
		return m, err
	}
	for _, f := range m.Files {
		fh, err := os.Open(filepath.Join(src, filepath.FromSlash(f.Path)))
		if err != nil {
			return m, err
		}
		mode := int64(0o644)
		if strings.HasPrefix(f.Path, "bin/") {
			mode = 0o755
		}
		err = put("files/"+f.Path, mode, f.Size, fh)
		_ = fh.Close()
		if err != nil {
			return m, err
		}
	}
	if err := tw.Close(); err != nil {
		return m, err
	}
	return m, gz.Close()
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// VerifyManifest checks the signature and contents of manifest bytes.
func VerifyManifest(manRaw, sigRaw []byte, keys Keys) (Manifest, error) {
	var m Manifest
	var sig Signature
	if err := json.Unmarshal(sigRaw, &sig); err != nil {
		return m, errors.New("bundle signature is unreadable")
	}
	pub, ok := keys[sig.KeyID]
	if !ok {
		return m, fmt.Errorf("bundle is signed by key %s, which this host does not trust", sig.KeyID)
	}
	raw, err := base64.StdEncoding.DecodeString(sig.Signature)
	if err != nil || !ed25519.Verify(pub, manRaw, raw) {
		return m, errors.New("bundle signature does not match its manifest")
	}
	if err := json.Unmarshal(manRaw, &m); err != nil {
		return m, errors.New("bundle manifest is unreadable")
	}
	return m, m.validate()
}

// Open verifies a bundle and extracts it into dest, which must not exist.
// Nothing is left behind unless every file matches the signed manifest.
func Open(bundle io.Reader, keys Keys, dest string) (Manifest, error) {
	var m Manifest
	stage := dest + ".partial"
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return m, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(stage)
		}
	}()
	gz, err := gzip.NewReader(io.LimitReader(bundle, maxBundleBytes+maxMetaBytes))
	if err != nil {
		return m, errors.New("not a live update bundle")
	}
	tr := tar.NewReader(gz)
	var manRaw, sigRaw []byte
	written := map[string]bool{}
	count := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, fmt.Errorf("bundle is damaged: %w", err)
		}
		count++
		if count > maxFiles+2 {
			return m, errors.New("bundle has too many files")
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return m, fmt.Errorf("bundle entry %q is not a regular file", h.Name)
		}
		switch h.Name {
		case "manifest.json":
			manRaw, err = io.ReadAll(io.LimitReader(tr, maxMetaBytes))
		case "manifest.sig":
			sigRaw, err = io.ReadAll(io.LimitReader(tr, maxMetaBytes))
		default:
			rel, found := strings.CutPrefix(h.Name, "files/")
			if !found || !allowedPath(rel) || written[rel] {
				return m, fmt.Errorf("bundle entry %q is not allowed", h.Name)
			}
			written[rel] = true
			mode := os.FileMode(0o644)
			if strings.HasPrefix(rel, "bin/") {
				mode = 0o755
			}
			err = writeEntry(filepath.Join(stage, "files", filepath.FromSlash(rel)), tr, h.Size, mode)
		}
		if err != nil {
			return m, err
		}
	}
	if manRaw == nil || sigRaw == nil {
		return m, errors.New("bundle has no signed manifest")
	}
	m, err = VerifyManifest(manRaw, sigRaw, keys)
	if err != nil {
		return m, err
	}
	if len(written) != len(m.Files) {
		return m, errors.New("bundle files do not match its manifest")
	}
	for _, f := range m.Files {
		sum, size, err := hashFile(filepath.Join(stage, "files", filepath.FromSlash(f.Path)))
		if err != nil || sum != f.SHA256 || size != f.Size {
			return m, fmt.Errorf("bundle file %q does not match its checksum", f.Path)
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), manRaw, 0o644); err != nil {
		return m, err
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.sig"), sigRaw, 0o644); err != nil {
		return m, err
	}
	if err := os.Rename(stage, dest); err != nil {
		return m, err
	}
	ok = true
	return m, nil
}

func writeEntry(p string, r io.Reader, size int64, mode os.FileMode) error {
	if size < 0 || size > maxBundleBytes {
		return errors.New("bundle entry is too large")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(f, r, size); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// The umask may have narrowed the mode; binaries must run.
	return os.Chmod(p, mode)
}
