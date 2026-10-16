package backuphost

import (
	"context"
	"strings"
	"testing"
)

func TestPlatformObjectsStayUnderTheirPrefix(t *testing.T) {
	h := &Host{}
	spec := TargetSpec{Kind: "r2", Endpoint: "https://x.invalid", Bucket: "b"}
	for _, key := range []string{"backups/packs/x.pack", "ndl-platform/../backups/x", ""} {
		if _, err := h.platformObject(context.Background(), Request{Target: spec, ObjectOp: "get", ObjectKey: key}); err == nil || !strings.Contains(err.Error(), "ndl-platform/") {
			t.Fatalf("%q must be refused: %v", key, err)
		}
	}
	if _, err := h.platformObject(context.Background(), Request{Target: TargetSpec{Kind: "local", Locator: t.TempDir()}, ObjectOp: "get", ObjectKey: "ndl-platform/pointer.json"}); err == nil {
		t.Fatal("only object storage holds the platform backup")
	}
}
