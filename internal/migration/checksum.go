package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ChecksumFile(path string) (string, int64, error) {
	f, err := os.Open(path)
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

func VerifyChecksum(path, want string) error {
	if want == "" {
		return fmt.Errorf("checksum is required")
	}
	got, _, err := ChecksumFile(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch")
	}
	return nil
}

func RelJail(root, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", fmt.Errorf("absolute path refused")
	}
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("path traversal refused")
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("path traversal refused")
	}
	joined := filepath.Join(root, clean)
	rootClean := filepath.Clean(root)
	if joined != rootClean && !strings.HasPrefix(joined, rootClean+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escape refused")
	}
	return joined, nil
}
