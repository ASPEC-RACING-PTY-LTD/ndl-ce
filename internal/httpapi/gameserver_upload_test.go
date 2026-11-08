package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

// zeros is n bytes of nothing, without allocating them.
type zeros struct{ n int64 }

func (z *zeros) Read(p []byte) (int, error) {
	if z.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.n {
		p = p[:z.n]
	}
	clear(p)
	z.n -= int64(len(p))
	return len(p), nil
}

func TestGameFileUploadStreamsLargeFiles(t *testing.T) {
	dir := t.TempDir()
	s, mem, token := testServer(t)
	s.Game = gameserver.NewRuntime(dir)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	data := filepath.Join(dir, "gameservers", "gs-up")
	_ = os.MkdirAll(filepath.Join(data, "garrysmod/maps"), 0o750)
	_ = mem.CreateGameServer(context.Background(), appdb.GameServer{
		ID: "gs-up", ClusterID: cluster.ID, Name: "Up", TemplateID: "ndl-mindustry", Status: "ready",
		DataDir: data, EnvJSON: []byte(`{}`), PortsJSON: []byte(`[]`), Capabilities: []byte(`["files"]`), CreatedAt: time.Now().UTC(),
	})

	upload := func(path string, size int64) (int, map[string]any) {
		t.Helper()
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		go func() {
			_ = mw.WriteField("path", path)
			fw, _ := mw.CreateFormFile("file", filepath.Base(path))
			_, err := io.Copy(fw, &zeros{n: size})
			if err == nil {
				err = mw.Close()
			}
			_ = pw.CloseWithError(err)
		}()
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/game-servers/gs-up/files/upload", pr)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, err := ts.Client().Do(req)
		if err != nil {
			return 0, nil
		}
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}

	// Past the old 64 MiB cap, written in place with no partial file left.
	const size = 100 << 20
	code, out := upload("garrysmod/maps/big.bsp", size)
	if code != 201 || out["bytes"].(float64) != size {
		t.Fatalf("upload %d %v", code, out)
	}
	st, err := os.Stat(filepath.Join(data, "garrysmod/maps/big.bsp"))
	if err != nil || st.Size() != size {
		t.Fatalf("uploaded file: %v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(data, "garrysmod/maps/big.bsp.part")); !os.IsNotExist(err) {
		t.Fatalf("partial file left behind: %v", err)
	}

	// Paths stay inside the server folder.
	if code, _ := upload("../escape.bsp", 10); code != 400 {
		t.Fatalf("escape upload = %d", code)
	}
}

func TestLocalHostWriteFileFromRefusesOverMax(t *testing.T) {
	h := gameserver.NewLocalHost(t.TempDir())
	if _, err := h.WriteFileFrom(context.Background(), "gs1", "a.bin", &zeros{n: 11}, 10); err == nil {
		t.Fatal("over-limit write accepted")
	}
	root, _ := h.Jail("gs1")
	if _, err := os.Stat(filepath.Join(root, "a.bin")); !os.IsNotExist(err) {
		t.Fatalf("over-limit file kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.bin.part")); !os.IsNotExist(err) {
		t.Fatalf("partial file kept: %v", err)
	}
	if n, err := h.WriteFileFrom(context.Background(), "gs1", "a.bin", &zeros{n: 10}, 10); err != nil || n != 10 {
		t.Fatalf("at-limit write = %d %v", n, err)
	}
}
