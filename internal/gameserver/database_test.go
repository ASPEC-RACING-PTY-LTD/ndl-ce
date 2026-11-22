package gameserver

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeDocker struct {
	mu      sync.Mutex
	calls   []string
	running map[string]bool // container name -> running
	dump    string
}

func (f *fakeDocker) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, joined)
	switch {
	case strings.HasPrefix(joined, "inspect -f {{.State.Running}} "):
		c := strings.TrimPrefix(joined, "inspect -f {{.State.Running}} ")
		r, ok := f.running[c]
		if !ok {
			return []byte("No such object"), &exec.ExitError{}
		}
		if r {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case strings.HasPrefix(joined, "network inspect"):
		return nil, &exec.ExitError{}
	case strings.HasPrefix(joined, "run -d --name ndl-gsdb-"):
		f.running[strings.Fields(joined)[3]] = true
		return []byte("dbcid"), nil
	case strings.HasPrefix(joined, "exec ndl-gsdb-") && strings.Contains(joined, "mariadb-dump"):
		return []byte(f.dump), nil
	case strings.HasPrefix(joined, "create"), strings.HasPrefix(joined, "run"):
		return []byte("gamecid"), nil
	}
	return []byte("ok"), nil
}

func (f *fakeDocker) has(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func newDBHost(t *testing.T) (*LocalHost, *fakeDocker) {
	f := &fakeDocker{running: map[string]bool{}, dump: "CREATE TABLE players (id int);\n"}
	rt := NewRuntime(t.TempDir())
	rt.Run = f.run
	return &LocalHost{RT: rt}, f
}

func TestDatabaseConfigureGeneratesAndKeepsCredentials(t *testing.T) {
	h, _ := newDBHost(t)
	ctx := context.Background()
	if _, err := h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "bad-name;"}); err == nil {
		t.Fatal("unsafe name accepted")
	}
	if _, err := h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "mysql"}); err == nil {
		t.Fatal("system database name accepted")
	}
	r, err := h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "jvs"})
	if err != nil {
		t.Fatal(err)
	}
	c := r.Config
	if r.State != "configured" || c.Database != "jvs" || !strings.HasPrefix(c.User, "game_") || len(c.Password) != 32 || len(c.RootPassword) != 40 || c.Host != "mariadb" || c.Port != 3306 {
		t.Fatalf("config %+v state %s", c, r.State)
	}
	st, err := os.Stat(h.RT.dbConfigPath("srv1"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file mode %v %v", st, err)
	}
	// Renaming before install keeps the generated credentials.
	r2, _ := h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "jvs2"})
	if r2.Config.Password != c.Password || r2.Config.User != c.User {
		t.Fatal("credentials changed on rename")
	}
}

func TestDatabaseInstallKeepsSecretsOffTheCommandLine(t *testing.T) {
	h, f := newDBHost(t)
	ctx := context.Background()
	r, _ := h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "jvs"})
	pw, root := r.Config.Password, r.Config.RootPassword
	r, err := h.Database(ctx, "srv1", DBCall{Op: "install"})
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "running" || r.Config.InstalledAt == nil {
		t.Fatalf("after install %+v", r)
	}
	for _, want := range []string{"network create --internal", "pull mariadb:11.4", "--network-alias mariadb", "--restart unless-stopped"} {
		if !f.has(want) {
			t.Errorf("missing docker call with %q", want)
		}
	}
	for _, c := range f.calls {
		if strings.Contains(c, pw) || strings.Contains(c, root) {
			t.Fatalf("a password reached the command line: %s", c)
		}
		if strings.Contains(c, " -p ") && strings.Contains(c, "ndl-gsdb") {
			t.Fatalf("the database publishes a port: %s", c)
		}
	}
	if _, err := h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "other"}); err == nil {
		t.Fatal("renamed after install")
	}
}

func TestGameJoinsItsDatabaseNetworkBeforeStarting(t *testing.T) {
	h, f := newDBHost(t)
	ctx := context.Background()
	_, _ = h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "jvs"})
	if _, err := h.Database(ctx, "srv1", DBCall{Op: "install"}); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if _, err := h.Start(ctx, RunSpec{ID: "srv1", Image: "img", Startup: "./run"}); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range f.calls {
		switch {
		case strings.HasPrefix(c, "create -i --name ndl-gs-srv1"):
			order = append(order, "create")
		case c == "network connect ndl-gsdb-srv1 ndl-gs-srv1":
			order = append(order, "connect")
		case c == "start ndl-gs-srv1":
			order = append(order, "start")
		}
	}
	if strings.Join(order, ",") != "create,connect,start" {
		t.Fatalf("start order %v; calls %v", order, f.calls)
	}
	// A server with no database starts the usual way.
	h2, f2 := newDBHost(t)
	if _, err := h2.Start(ctx, RunSpec{ID: "srv2", Image: "img", Startup: "./run"}); err != nil {
		t.Fatal(err)
	}
	if !f2.has("run -d -i --name ndl-gs-srv2") || f2.has("create") {
		t.Fatalf("plain start changed: %v", f2.calls)
	}
}

func TestBackupCarriesTheDatabaseDump(t *testing.T) {
	h, f := newDBHost(t)
	ctx := context.Background()
	_, _ = h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "jvs"})
	if _, err := h.Database(ctx, "srv1", DBCall{Op: "install"}); err != nil {
		t.Fatal(err)
	}
	dir, _ := h.jail("srv1")
	_ = os.WriteFile(filepath.Join(dir, "server.cfg"), []byte("x"), 0o644)
	file, err := h.Backup(ctx, "srv1", "b1")
	if err != nil {
		t.Fatal(err)
	}
	names := archiveNames(t, file.Path)
	if len(names) < 3 || names[0] != ".ndl-backup.json" || !contains(names, dbDumpEntry) || !contains(names, "server.cfg") {
		t.Fatalf("archive %v", names)
	}
	if got := readEntry(t, file.Path, dbDumpEntry); got != f.dump {
		t.Fatalf("dump in archive %q", got)
	}
	if _, err := os.Stat(file.Path + ".db.sql"); !os.IsNotExist(err) {
		t.Fatal("temporary dump left behind")
	}

	// A stopped database fails the backup rather than leave it out.
	f.running["ndl-gsdb-srv1"] = false
	if _, err := h.Backup(ctx, "srv1", "b2"); err == nil || !strings.Contains(err.Error(), "start it") {
		t.Fatalf("backup with the database stopped: %v", err)
	}

	// Restoring needs the database up, and checks before touching files.
	_ = os.WriteFile(filepath.Join(dir, "server.cfg"), []byte("changed"), 0o644)
	if err := h.RestoreBackup(ctx, "srv1", "b1"); err == nil {
		t.Fatal("restore went ahead with the database stopped")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "server.cfg")); string(b) != "changed" {
		t.Fatal("files changed before the database check")
	}
	f.running["ndl-gsdb-srv1"] = true
	f.calls = nil
	if err := h.RestoreBackup(ctx, "srv1", "b1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "server.cfg")); string(b) != "x" {
		t.Fatal("files not restored")
	}
	if !f.has("exec -i ndl-gsdb-srv1 sh -c") {
		t.Fatalf("dump not loaded: %v", f.calls)
	}
}

func TestDeletingTheServerDeletesItsDatabase(t *testing.T) {
	h, f := newDBHost(t)
	ctx := context.Background()
	_, _ = h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "jvs"})
	_, _ = h.Database(ctx, "srv1", DBCall{Op: "install"})
	if err := h.RemoveData(ctx, "srv1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.RT.dbDir("srv1")); !os.IsNotExist(err) {
		t.Fatal("database data left behind")
	}
	if !f.has("rm -f ndl-gsdb-srv1") || !f.has("network rm ndl-gsdb-srv1") {
		t.Fatalf("container or network left: %v", f.calls)
	}
}

func TestUninstallKeepsDataUnlessAsked(t *testing.T) {
	h, _ := newDBHost(t)
	ctx := context.Background()
	_, _ = h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "jvs"})
	_, _ = h.Database(ctx, "srv1", DBCall{Op: "install"})
	r, err := h.Database(ctx, "srv1", DBCall{Op: "remove"})
	if err != nil || r.State != "configured" || r.Config == nil {
		t.Fatalf("uninstall %+v %v", r, err)
	}
	r, err = h.Database(ctx, "srv1", DBCall{Op: "remove", DeleteData: true})
	if err != nil || r.State != "none" {
		t.Fatalf("delete %+v %v", r, err)
	}
}

// The load command's quoting, run through a real shell with a stand-in
// for the mariadb client.
func TestDatabaseLoadCommandQuoting(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	script := "mariadb() { printf '[%s]' \"$@\"; echo; }; " + strings.ReplaceAll(dbLoadC, "exec mariadb", "mariadb")
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = []string{"MARIADB_ROOT_PASSWORD=r00t", "MARIADB_DATABASE=jvs_db", "MARIADB_USER=game_ab12"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	want := "[-uroot][-e][DROP DATABASE IF EXISTS `jvs_db`; CREATE DATABASE `jvs_db`; GRANT ALL ON `jvs_db`.* TO 'game_ab12'@'%';]\n[-uroot][jvs_db]\n"
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func readEntry(t *testing.T, archive, name string) string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("%s not found: %v", name, err)
		}
		if h.Name == name {
			b, _ := io.ReadAll(tr)
			return string(b)
		}
	}
}

func TestDatabaseResourcesLimitsAndConfig(t *testing.T) {
	h, f := newDBHost(t)
	ctx := context.Background()
	_, _ = h.Database(ctx, "srv1", DBCall{Op: "configure", Database: "jvs"})
	if _, err := h.Database(ctx, "srv1", DBCall{Op: "install"}); err != nil {
		t.Fatal(err)
	}
	if !f.has("-v " + h.RT.dbConfDir("srv1") + ":/etc/mysql/conf.d:ro") {
		t.Fatalf("install did not mount the config: %v", f.calls)
	}
	for _, bad := range []DBResources{{MemoryMB: 100}, {MemoryMB: 1024, CPUs: -1}, {MemoryMB: 1024, BufferPoolMB: 1000}} {
		if _, err := h.Database(ctx, "srv1", DBCall{Op: "resources", Resources: &bad}); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	f.calls = nil
	if _, err := h.Database(ctx, "srv1", DBCall{Op: "resources", Resources: &DBResources{MemoryMB: 2048, CPUs: 0}}); err != nil {
		t.Fatal(err)
	}
	if !f.has("update --memory 2048m --memory-swap 2048m --cpus ") || f.has("--cpus 0 ") {
		t.Fatalf("update %v", f.calls)
	}
	conf, _ := os.ReadFile(filepath.Join(h.RT.dbConfDir("srv1"), confFileName))
	if !strings.Contains(string(conf), "innodb_buffer_pool_size = 1024M") {
		t.Fatalf("conf %q", conf)
	}
	if got := bufferPoolFor(&DBConfig{MemoryMB: 300}); got != 128 {
		t.Fatalf("small pool %d", got)
	}
}
