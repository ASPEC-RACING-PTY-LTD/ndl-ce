package ctbackup

import (
	"strings"
	"testing"
)

func TestStreamTarDoesNotLaunchCompressor(t *testing.T) {
	args := tarPackArgs("/var/lib/ndl/storage/local/volumes/container-root/x")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "zstd") || strings.Contains(joined, "--zstd") {
		t.Fatalf("stream tar must not launch zstd: %v", args)
	}
	if strings.Contains(joined, "-z") || strings.Contains(joined, "--gzip") {
		t.Fatalf("stream tar must not gzip in-process: %v", args)
	}
	hasStdout := false
	for i, a := range args {
		if a == "-cf" && i+1 < len(args) && args[i+1] == "-" {
			hasStdout = true
		}
	}
	if !hasStdout {
		t.Fatalf("stream tar must write to stdout: %v", args)
	}
}
