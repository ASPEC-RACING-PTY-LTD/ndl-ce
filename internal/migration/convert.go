package migration

import (
	"fmt"
	"path/filepath"
	"strings"
)

var (
	qemuImgBin = "/usr/bin/qemu-img"
	allowedSrc = map[string]struct{}{
		"qcow2": {},
		"raw":   {},
		"vmdk":  {},
		"vpc":   {},
		"vhdx":  {},
	}
	allowedDst = map[string]struct{}{
		"qcow2": {},
		"raw":   {},
	}
)

// ConvertArgv builds a typed qemu-img convert command. Dest is always qcow2 or raw.
func ConvertArgv(srcPath, srcFormat, dstPath, dstFormat string) ([]string, error) {
	if err := validateImagePath(srcPath); err != nil {
		return nil, err
	}
	if err := validateImagePath(dstPath); err != nil {
		return nil, err
	}
	srcFormat = NormalizeFormat(srcFormat, srcPath)
	dstFormat = NormalizeFormat(dstFormat, dstPath)
	if _, ok := allowedSrc[srcFormat]; !ok {
		return nil, fmt.Errorf("source format %s cannot currently be read", srcFormat)
	}
	if _, ok := allowedDst[dstFormat]; !ok {
		return nil, fmt.Errorf("destination format must be qcow2 or raw")
	}
	return []string{qemuImgBin, "convert", "-p", "-f", srcFormat, "-O", dstFormat, srcPath, dstPath}, nil
}

func InfoArgv(path string) ([]string, error) {
	if err := validateImagePath(path); err != nil {
		return nil, err
	}
	return []string{qemuImgBin, "info", "--output=json", path}, nil
}

func CheckArgv(path string) ([]string, error) {
	if err := validateImagePath(path); err != nil {
		return nil, err
	}
	return []string{qemuImgBin, "check", path}, nil
}

func NormalizeFormat(format, path string) string {
	f := strings.ToLower(strings.TrimPrefix(format, "."))
	if f == "vhd" {
		return "vpc"
	}
	if f == "img" {
		return "raw"
	}
	if f != "" {
		return f
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "qcow2", "qcow":
		return "qcow2"
	case "raw", "img":
		return "raw"
	case "vmdk":
		return "vmdk"
	case "vhd":
		return "vpc"
	case "vhdx":
		return "vhdx"
	default:
		return "qcow2"
	}
}

func FormatSupported(format string, have map[string]bool) bool {
	n := NormalizeFormat(format, "")
	if n == "qcow2" || n == "raw" {
		return true
	}
	if have == nil {
		return n == "vmdk" || n == "vpc" || n == "vhdx"
	}
	return have[n]
}

func ValidateHostPath(p string) error {
	return validateImagePath(p)
}

func validateImagePath(p string) error {
	if p == "" || !strings.HasPrefix(p, "/") {
		return fmt.Errorf("image path must be absolute")
	}
	if strings.Contains(p, "..") || strings.ContainsAny(p, ",=\n\r\x00;$") {
		return fmt.Errorf("image path is invalid")
	}
	clean := filepath.Clean(p)
	if clean != p {
		return fmt.Errorf("image path is not clean")
	}
	ok := strings.HasPrefix(clean, "/var/lib/ndl/storage/") ||
		strings.HasPrefix(clean, StagingRoot+"/") ||
		strings.HasPrefix(clean, "/tmp/")
	if !ok {
		return fmt.Errorf("image path must be under No-dal storage, migration staging, or a test temp directory")
	}
	return nil
}
