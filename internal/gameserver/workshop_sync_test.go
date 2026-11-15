package gameserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildGMA writes a version 3 GMA holding files (name -> body).
func buildGMA(t *testing.T, files map[string]string, crc bool) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GMAD")
	b.WriteByte(3)
	b.Write(make([]byte, 16))
	b.WriteByte(0) // no required content
	b.WriteString("Test pack\x00desc\x00author\x00")
	_ = binary.Write(&b, binary.LittleEndian, int32(1))
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	for i, n := range names {
		_ = binary.Write(&b, binary.LittleEndian, uint32(i+1))
		b.WriteString(n + "\x00")
		_ = binary.Write(&b, binary.LittleEndian, int64(len(files[n])))
		_ = binary.Write(&b, binary.LittleEndian, uint32(0))
	}
	_ = binary.Write(&b, binary.LittleEndian, uint32(0))
	for _, n := range names {
		b.WriteString(files[n])
	}
	if crc {
		b.Write([]byte{1, 2, 3, 4})
	}
	return b.Bytes()
}

type fakeSteam struct {
	ids   []string
	files map[string]WorkshopFile
}

func (f *fakeSteam) Collection(context.Context, string) ([]string, error) { return f.ids, nil }
func (f *fakeSteam) Files(context.Context, []string) (map[string]WorkshopFile, error) {
	return f.files, nil
}

type fixture struct {
	t      *testing.T
	steam  *fakeSteam
	packs  map[string][]byte
	asked  []string
	dir    *SyncDir
	base   string
	engine WorkshopSync
	st     SyncState
}

func newSyncFixture(t *testing.T) *fixture {
	f := &fixture{t: t, steam: &fakeSteam{files: map[string]WorkshopFile{}}, packs: map[string][]byte{}, base: t.TempDir()}
	f.engine = WorkshopSync{
		API: f.steam,
		Fetch: func(_ context.Context, ids []string, dir string) (map[string]string, map[string]string) {
			got, failed := map[string]string{}, map[string]string{}
			for _, id := range ids {
				f.asked = append(f.asked, id)
				body, ok := f.packs[id]
				if !ok {
					failed[id] = "Timeout"
					continue
				}
				p := filepath.Join(dir, id+".bin")
				_ = os.WriteFile(p, body, 0o644)
				got[id] = p
			}
			return got, failed
		},
		Now:  func() time.Time { return time.Unix(1731700123, 0) },
		Work: t.TempDir(),
	}
	d, err := OpenSyncDir(f.base, SyncDirRel)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	f.dir = d
	return f
}

func (f *fixture) item(id string, updated int64, files map[string]string) {
	f.steam.ids = append(f.steam.ids, id)
	f.steam.files[id] = WorkshopFile{ID: id, Title: "Pack " + id, TimeUpdated: updated, Size: 10, Available: true, AppID: 4000}
	f.packs[id] = buildGMA(f.t, files, true)
}

func (f *fixture) status(s SyncStatus) {
	raw, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(f.base, SyncDirRel, SyncStatusFile), raw, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) check(req string) SyncManifest {
	f.t.Helper()
	f.asked = nil
	m, err := f.engine.Check(context.Background(), &f.st, f.dir, "3520969473", req)
	if err != nil {
		f.t.Fatal(err)
	}
	var onDisk SyncManifest
	raw, _ := os.ReadFile(filepath.Join(f.base, SyncDirRel, SyncManifestFile))
	if err := json.Unmarshal(raw, &onDisk); err != nil || onDisk.Seq != m.Seq {
		f.t.Fatalf("manifest on disk %s", raw)
	}
	return m
}

func TestWorkshopSyncDeliversNewAndUpdatedItems(t *testing.T) {
	f := newSyncFixture(t)
	f.item("100", 1000, map[string]string{"materials/a.vmt": "x"})
	f.item("200", 2000, map[string]string{"lua/autorun/b.lua": "print(1)"})
	f.item("300", 3000, map[string]string{"gamemodes/x/gamemode/init.lua": "y"})
	// The server loaded 100 at 1000 and 200 at an older version; 300 is new.
	f.status(SyncStatus{Version: 1, Items: []SyncStatusItem{{WSID: "100", TimeUpdated: 1000, State: "in_collection"}, {WSID: "200", TimeUpdated: 1500, State: "in_collection"}}})

	m := f.check("1731700000-4821")
	if m.Seq != 1 || m.RequestID == nil || *m.RequestID != "1731700000-4821" || m.Collection != "3520969473" || m.Version != 1 {
		t.Fatalf("manifest %+v", m)
	}
	if strings.Join(f.asked, ",") != "200,300" {
		t.Fatalf("only changed items are downloaded: %v", f.asked)
	}
	got := map[string]SyncManifestItem{}
	for _, it := range m.Items {
		got[it.WSID] = it
	}
	if got["200"].State != "updated" || got["200"].Kind != "lua" || got["300"].State != "new" || got["300"].Kind != "lua" {
		t.Fatalf("labels %+v", m.Items)
	}
	body, err := os.ReadFile(filepath.Join(f.base, SyncDirRel, got["300"].File))
	if err != nil || !bytes.HasPrefix(body, []byte("GMAD")) || got["300"].Size != int64(len(body)) || len(got["300"].SHA256) != 64 {
		t.Fatalf("pack on disk %v %+v", err, got["300"])
	}

	// Nothing acknowledged yet: a scheduled check lists them again, downloads nothing.
	m = f.check("")
	if m.Seq != 2 || m.RequestID != nil || len(m.Items) != 2 || len(f.asked) != 0 {
		t.Fatalf("second check %+v asked %v", m, f.asked)
	}
	// The server mounted both: the list empties.
	f.status(SyncStatus{Version: 1, Items: []SyncStatusItem{
		{WSID: "100", TimeUpdated: 1000, State: "in_collection"}, {WSID: "200", TimeUpdated: 2000, State: "lua_restart"}, {WSID: "300", TimeUpdated: 3000, State: "mounted"},
	}})
	if m = f.check(""); len(m.Items) != 0 || len(f.asked) != 0 {
		t.Fatalf("acknowledged items stay out: %+v", m)
	}
	// After a restart the server loads 300 from the Workshop: its pack is deleted.
	f.status(SyncStatus{Version: 1, Items: []SyncStatusItem{{WSID: "300", TimeUpdated: 3000, State: "in_collection"}, {WSID: "200", TimeUpdated: 2000, State: "mounted"}}})
	f.check("")
	if _, err := os.Stat(filepath.Join(f.base, SyncDirRel, "items", "300.gma")); !os.IsNotExist(err) {
		t.Fatal("a pack the server loads itself must be deleted")
	}
	if _, err := os.Stat(filepath.Join(f.base, SyncDirRel, "items", "200.gma")); err != nil {
		t.Fatal("a pack the server has not marked in_collection must stay")
	}
}

func TestWorkshopSyncBlocklistAndErrors(t *testing.T) {
	f := newSyncFixture(t)
	f.item("1485877786", 10, map[string]string{"lua/x.lua": "bad"})
	f.item("555", 10, map[string]string{"a.txt": "a"})
	f.item("777", 10, map[string]string{"b.txt": "b"})
	delete(f.packs, "777") // download fails
	f.steam.ids = append(f.steam.ids, "999")
	f.status(SyncStatus{Version: 1, Blocked: []string{"555", "../etc"}})
	m := f.check("")
	errs := map[string]string{}
	for _, e := range m.Errors {
		errs[e.WSID] = e.Error
	}
	if errs["1485877786"] != "blocked" || errs["555"] != "blocked" || !strings.Contains(errs["777"], "download failed") || errs["999"] == "" || len(m.Items) != 0 {
		t.Fatalf("errors %+v items %+v", m.Errors, m.Items)
	}
	for _, id := range f.asked {
		if id == "1485877786" || id == "555" {
			t.Fatalf("blocked item %s was downloaded", id)
		}
	}
	// The server can add to the blocklist, never take from it.
	f.status(SyncStatus{Version: 1})
	if m = f.check(""); m.Errors[0].Error != "blocked" || len(f.st.Blocked) != 1 || f.st.Blocked[0] != "555" {
		t.Fatalf("blocklist %+v %v", m.Errors, f.st.Blocked)
	}
}

// Without a status.json the collection as it was at server start is what
// the server runs.
func TestWorkshopSyncBaselineWithoutStatus(t *testing.T) {
	f := newSyncFixture(t)
	f.item("100", 1000, map[string]string{"a.vmt": "x"})
	if err := f.engine.Baseline(context.Background(), &f.st, "1"); err != nil {
		t.Fatal(err)
	}
	if m := f.check(""); len(m.Items) != 0 || len(f.asked) != 0 {
		t.Fatalf("nothing changed since start: %+v", m)
	}
	f.steam.files["100"] = WorkshopFile{ID: "100", Title: "Pack", TimeUpdated: 1001, Available: true}
	if m := f.check(""); len(m.Items) != 1 || m.Items[0].State != "updated" || m.Items[0].Kind != "assets" {
		t.Fatalf("an update after start: %+v", m)
	}
}

// The server's folder is the game's to change; ndl-ce never follows a link
// out of it.
func TestSyncDirRefusesLinks(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	_ = os.MkdirAll(filepath.Join(base, "garrysmod", "data"), 0o755)
	_ = os.Symlink(outside, filepath.Join(base, SyncDirRel))
	if _, err := OpenSyncDir(base, SyncDirRel); err == nil {
		t.Fatal("a linked cs_sync folder must be refused")
	}
	_ = os.Remove(filepath.Join(base, SyncDirRel))
	d, err := OpenSyncDir(base, SyncDirRel)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	victim := filepath.Join(outside, "victim")
	_ = os.WriteFile(victim, []byte("keep"), 0o644)
	_ = os.Symlink(victim, filepath.Join(base, SyncDirRel, "manifest.json.tmp"))
	_ = os.Symlink(victim, filepath.Join(base, SyncDirRel, SyncStatusFile))
	if err := d.WriteAtomic("manifest.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatal("a write went through a link")
	}
	if _, _, err := d.Read(SyncStatusFile, 1024); err == nil {
		t.Fatal("a linked status.json must not be read")
	}
	_ = os.Symlink(outside, filepath.Join(base, SyncDirRel, "items"))
	if _, err := d.Sub("items"); err == nil {
		t.Fatal("a linked items folder must be refused")
	}
}

func TestReadGMA(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.gma")
	for _, crc := range []bool{true, false} {
		_ = os.WriteFile(p, buildGMA(t, map[string]string{"materials/x.vmt": "abc", "LUA/Y.LUA": "z"}, crc), 0o644)
		info, err := ReadGMA(p)
		if err != nil || len(info.Files) != 2 || !info.HasLua() || info.Name != "Test pack" {
			t.Fatalf("crc=%v: %+v %v", crc, info, err)
		}
	}
	good := buildGMA(t, map[string]string{"a": "abc"}, true)
	_ = os.WriteFile(p, good[:len(good)-6], 0o644)
	if _, err := ReadGMA(p); err == nil {
		t.Fatal("a cut-short pack must be refused")
	}
	_ = os.WriteFile(p, []byte("PK\x03\x04not a gma"), 0o644)
	if _, err := ReadGMA(p); err == nil {
		t.Fatal("a non-GMA must be refused")
	}
}

// Steam stores older items LZMA-compressed with the size in the header and
// no end marker; they unpack to the GMA.
func TestUnpackLegacyWorkshopFile(t *testing.T) {
	if _, err := os.Stat(lzmaBin); err != nil {
		t.Skip("xz is not installed")
	}
	gma := buildGMA(t, map[string]string{"lua/a.lua": strings.Repeat("print(1)\n", 200)}, true)
	cmd := exec.Command(lzmaBin, "--format=lzma", "--compress", "--stdout")
	cmd.Stdin = bytes.NewReader(gma)
	packed, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint64(packed[5:13], uint64(len(gma)))
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "x_legacy.bin"), filepath.Join(dir, "x.gma")
	_ = os.WriteFile(src, packed, 0o644)
	if err := unpackWorkshopFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, gma) {
		t.Fatal("unpacked pack differs")
	}
	// A plain GMA download is taken as it is.
	_ = os.WriteFile(src, gma, 0o644)
	_ = os.Remove(dst)
	if err := unpackWorkshopFile(src, dst); err != nil {
		t.Fatal(err)
	}
}
