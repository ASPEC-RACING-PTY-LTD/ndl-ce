package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/storage"
)

const (
	VerifyUnverified  = "unverified"
	VerifyVerified    = "verified"
	VerifyFailed      = "failed"
	VerifyUnavailable = "unavailable"
	extractSizeCap    = 1 << 20
)

// VerifyResult is an open-verify of a materialized backup. Checksum mismatch
// is unverified. qemu-img check is required before verified.
type VerifyResult struct {
	ObservedSHA256 string `json:"observed_sha256,omitempty"`
	QEMUImgOK      bool   `json:"qemu_img_ok"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
}

// ExtractResult is a file taken from a received qcow2. Missing libguestfs stays unavailable.
type ExtractResult struct {
	GuestPath string `json:"guest_path"`
	DestPath  string `json:"dest_path,omitempty"`
	Size      int64  `json:"size_bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

// CheckOffline re-hashes a backup file and runs qemu-img check. It does not
// invent verified when qemu-img did not run.
func (e *Engine) CheckOffline(ctx context.Context, src, expectedSHA string) (VerifyResult, error) {
	if err := storage.AllowedArtifactPath(src); err != nil {
		if err := ValidateDiskPath(src); err != nil {
			return VerifyResult{}, fmt.Errorf("verify locator is invalid")
		}
	}
	sum, _, err := checksumFile(src)
	if err != nil {
		return VerifyResult{Status: VerifyUnverified, Reason: "backup file is unreadable"}, nil
	}
	out := VerifyResult{ObservedSHA256: sum, Status: VerifyUnverified}
	expected := strings.ToLower(strings.TrimSpace(expectedSHA))
	if expected == "" {
		out.Reason = "catalog checksum is missing"
		return out, nil
	}
	if expected != strings.ToLower(sum) {
		out.Reason = "checksum mismatch"
		return out, nil
	}
	if e != nil && e.SkipHostCmds {
		out.Reason = "qemu-img check was not executed"
		return out, nil
	}
	argv := []string{BinQEMUImg, "check", src}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		out.Status = VerifyFailed
		out.Reason = firstNonEmpty(strings.TrimSpace(string(raw)), "qemu-img check failed")
		return out, nil
	}
	out.QEMUImgOK = true
	out.Status = VerifyVerified
	return out, nil
}

// ExtractOffline copies one guest path from a received qcow2 using guestfish
// when installed. Missing tools stay unavailable. There is no generic host shell.
func (e *Engine) ExtractOffline(ctx context.Context, src, guestPath, dest string) (ExtractResult, error) {
	gp, err := jailGuestFile(guestPath)
	if err != nil {
		return ExtractResult{}, err
	}
	if err := storage.AllowedArtifactPath(dest); err != nil {
		return ExtractResult{}, err
	}
	if e != nil && e.SkipHostCmds {
		return ExtractResult{GuestPath: gp, Status: VerifyUnavailable, Reason: "libguestfs extract is not configured"}, nil
	}
	if _, err := exec.LookPath("guestfish"); err != nil {
		return ExtractResult{GuestPath: gp, Status: VerifyUnavailable, Reason: "libguestfs is not installed"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return ExtractResult{}, err
	}
	argv := []string{"guestfish", "--ro", "-a", src, "-i", "copy-out", gp, filepath.Dir(dest)}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return ExtractResult{GuestPath: gp, Status: VerifyFailed, Reason: firstNonEmpty(strings.TrimSpace(string(raw)), "guestfish copy-out failed")}, nil
	}
	copied := filepath.Join(filepath.Dir(dest), filepath.Base(gp))
	if copied != dest {
		if err := os.Rename(copied, dest); err != nil {
			return ExtractResult{GuestPath: gp, Status: VerifyFailed, Reason: "extracted file could not be placed"}, nil
		}
	}
	sum, size, err := checksumFile(dest)
	if err != nil {
		return ExtractResult{GuestPath: gp, Status: VerifyFailed, Reason: "extracted file is unreadable"}, nil
	}
	if size > extractSizeCap {
		_ = os.Remove(dest)
		return ExtractResult{GuestPath: gp, Status: VerifyFailed, Reason: "extracted file exceeds 1MiB restore-file cap"}, nil
	}
	return ExtractResult{GuestPath: gp, DestPath: dest, Size: size, SHA256: sum, Status: VerifyVerified}, nil
}

func jailGuestFile(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.ContainsAny(p, "\n\x00") {
		return "", fmt.Errorf("guest path is invalid")
	}
	return filepath.Clean(p), nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
