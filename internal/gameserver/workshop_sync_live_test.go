package gameserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWorkshopSyncLive runs a real check against Steam and SteamCMD:
// NDL_WORKSHOP_LIVE=<collection>:<small item in it> go test -run WorkshopSyncLive
// The item is treated as updated since the server started, so it is
// downloaded, unpacked and listed in the manifest.
func TestWorkshopSyncLive(t *testing.T) {
	spec := os.Getenv("NDL_WORKSHOP_LIVE")
	if spec == "" {
		t.Skip("set NDL_WORKSHOP_LIVE=<collection>:<item>")
	}
	collection, item := spec, ""
	for i := range spec {
		if spec[i] == ':' {
			collection, item = spec[:i], spec[i+1:]
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	work := t.TempDir()
	base := t.TempDir()
	engine := WorkshopSync{API: SteamWorkshopAPI{}, Fetch: NewRuntime(work).WorkshopDownload, Work: work}
	var st SyncState
	if err := engine.Baseline(ctx, &st, collection); err != nil {
		t.Fatal(err)
	}
	st.Baseline[item]-- // pretend the server runs the version before
	dir, err := OpenSyncDir(base, SyncDirRel)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	m, err := engine.Check(ctx, &st, dir, collection, "live-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Items) != 1 || m.Items[0].WSID != item || m.Items[0].State != "updated" {
		t.Fatalf("manifest %+v", m)
	}
	info, err := ReadGMA(filepath.Join(base, SyncDirRel, m.Items[0].File))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s %q: %d bytes, kind %s, %d files, sha256 %s, errors %v", item, m.Items[0].Title, m.Items[0].Size, m.Items[0].Kind, len(info.Files), m.Items[0].SHA256[:12], m.Errors)
}
