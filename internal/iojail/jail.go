package iojail

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	TargetHost = "host"
	TargetCT   = "system-container"
)

// HostDenyPrefixes are refused even for admin host Files.
var HostDenyPrefixes = []string{
	"/var/lib/ndl/host.key",
	"/var/lib/ndl/setup.token",
	"/var/lib/ndl/control",
	"/var/lib/ndl/agent",
	"/var/lib/ndl/secrets",
	"/var/lib/ndl/certs",
	"/var/lib/postgresql",
	"/etc/ndl",
}

// Request is a path operation beneath a jail root.
type Request struct {
	Root   string
	Rel    string
	Target string
}

// CleanRel rejects parent-directory and control-character segments. The empty
// path, /, and . mean the jail root.
func CleanRel(rel string) (string, error) {
	return cleanRel(rel)
}

func cleanRel(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "/" {
		return ".", nil
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	if strings.HasPrefix(rel, "/") {
		rel = strings.TrimPrefix(rel, "/")
	}
	if strings.Contains(rel, "\x00") {
		return "", fmt.Errorf("path contains a NUL")
	}
	for _, r := range rel {
		if r < 32 || r == 127 {
			return "", fmt.Errorf("path contains a control character")
		}
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes the jail")
	}
	return clean, nil
}

func deniedHost(root, abs string) error {
	abs = filepath.ToSlash(filepath.Clean(abs))
	for _, p := range HostDenyPrefixes {
		prefix := filepath.ToSlash(filepath.Clean(p))
		if abs == prefix || strings.HasPrefix(abs+"/", prefix+"/") {
			return fmt.Errorf("path is denied by host policy")
		}
	}
	return nil
}

func joinUnder(root, rel string) (string, error) {
	rel, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	if root == "" {
		return "", fmt.Errorf("jail root is required")
	}
	if rel == "." {
		return root, nil
	}
	out := filepath.Join(root, filepath.FromSlash(rel))
	relOut, err := filepath.Rel(root, out)
	if err != nil || strings.HasPrefix(relOut, "..") {
		return "", fmt.Errorf("path escapes the jail")
	}
	return out, nil
}
