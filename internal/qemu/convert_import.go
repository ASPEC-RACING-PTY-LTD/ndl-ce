package qemu

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const migrationRoot = "/var/lib/ndl/migration"

var importFormats = map[string]struct{}{
	"qcow2": {}, "raw": {}, "vmdk": {}, "vpc": {}, "vhdx": {},
}

// ConvertImport is a typed qemu-img convert for migration staging. Dest remains qcow2 or raw.
func (e *Engine) ConvertImport(ctx context.Context, req ConvertRequest) error {
	if err := validateMigrationOrStorage(req.SourcePath); err != nil {
		return err
	}
	if err := validateMigrationOrStorage(req.DestPath); err != nil {
		return err
	}
	if req.SourceFormat == "" {
		req.SourceFormat = "qcow2"
	}
	if req.DestFormat == "" {
		req.DestFormat = "qcow2"
	}
	if req.SourceFormat == "vhd" {
		req.SourceFormat = "vpc"
	}
	if _, ok := importFormats[req.SourceFormat]; !ok {
		return fmt.Errorf("source format must be a qemu-img readable disk format")
	}
	if req.DestFormat != "qcow2" && req.DestFormat != "raw" {
		return fmt.Errorf("dest format must be qcow2 or raw")
	}
	if err := e.AssertDiskOffline(ctx, req.DestPath); err != nil && !strings.HasPrefix(req.DestPath, migrationRoot) {
		return err
	}
	if e.SkipHostCmds {
		return fmt.Errorf("host commands skipped; qemu-img convert was not run")
	}
	argv := []string{BinQEMUImg, "convert", "-p", "-f", req.SourceFormat, "-O", req.DestFormat, req.SourcePath, req.DestPath}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img convert: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func validateMigrationOrStorage(p string) error {
	if err := ValidateDiskPath(p); err == nil {
		return nil
	}
	if p == "" || !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.ContainsAny(p, ",=\n\r\x00;$") {
		return fmt.Errorf("migration image path is invalid")
	}
	clean := filepath.Clean(p)
	if clean != p {
		return fmt.Errorf("migration image path is not clean")
	}
	if strings.HasPrefix(clean, migrationRoot+"/") || strings.HasPrefix(clean, "/tmp/") {
		return nil
	}
	return fmt.Errorf("disk_path must be under the storage root")
}
