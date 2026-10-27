package claudeagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeGrants(t *testing.T) {
	got, err := NormalizeGrants([]Grant{
		{Kind: KindGameServer, ID: "s1", Name: "JvS", Folders: []Folder{{Path: "/garrysmod/addons/", Write: true}, {Path: "garrysmod/gamemodes"}, {Path: "garrysmod/addons"}}, Log: true},
		{Kind: KindGameServer, ID: "s2", Name: "jvs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Slug != "jvs" || got[1].Slug != "jvs-2" {
		t.Fatalf("slugs %q %q", got[0].Slug, got[1].Slug)
	}
	if len(got[0].Folders) != 2 || got[0].Folders[0].Path != "garrysmod/addons" || !got[0].Folders[0].Write {
		t.Fatalf("folders %+v", got[0].Folders)
	}
	if Target(got[0], got[0].Folders[0]) != "/srv/jvs/garrysmod/addons" {
		t.Fatalf("target %s", Target(got[0], got[0].Folders[0]))
	}
	for want, g := range map[string]Grant{
		"outside the server":     {Kind: KindGameServer, ID: "a", Folders: []Folder{{Path: "../etc"}}},
		"not the whole server":   {Kind: KindGameServer, ID: "a", Folders: []Folder{{Path: "/"}}},
		"a mount cannot use":     {Kind: KindGameServer, ID: "a", Folders: []Folder{{Path: "my addons"}}},
		"is inside":              {Kind: KindGameServer, ID: "a", Folders: []Folder{{Path: "garrysmod"}, {Path: "garrysmod/addons"}}},
		"is not supported":       {Kind: "workload", ID: "a"},
		"needs its own resource": {Kind: KindGameServer},
	} {
		if _, err := NormalizeGrants([]Grant{g}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestClaudeMD(t *testing.T) {
	gs, _ := NormalizeGrants([]Grant{{Kind: KindGameServer, ID: "s1", Name: "JvS", Folders: []Folder{{Path: "garrysmod/addons", Write: true}, {Path: "garrysmod/data"}}, Console: true}})
	md := ClaudeMD(gs)
	for _, want := range []string{"`/srv/jvs/garrysmod/addons` is the server's `garrysmod/addons` (read-write)", "(read-only)", "`ndl console jvs COMMAND`"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
	if strings.Contains(md, "ndl log") || strings.Contains(md, "ndl restart") {
		t.Errorf("ungranted commands listed:\n%s", md)
	}
}

func TestHostGrantStaysInsideTheServer(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "gameservers", "s1")
	_ = os.MkdirAll(filepath.Join(data, "garrysmod", "addons"), 0o750)
	_ = os.MkdirAll(filepath.Join(root, "secret"), 0o750)
	_ = os.Symlink(filepath.Join(root, "secret"), filepath.Join(data, "garrysmod", "escape"))
	var calls [][]string
	h := Host{
		DataDir: func(id string) string { return filepath.Join(root, "gameservers", id) },
		SetFACL: func(_ context.Context, args ...string) ([]byte, error) { calls = append(calls, args); return nil, nil },
	}
	mounts, err := h.Grant(context.Background(), 101000, []HostACL{{ServerID: "s1", Path: "garrysmod/addons", Write: true}})
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(filepath.Join(data, "garrysmod", "addons"))
	if len(mounts) != 1 || mounts[0].Source != real {
		t.Fatalf("mounts %+v", mounts)
	}
	if strings.Join(calls[0], " ") != "-R -P -m u:101000:rwX,d:u:101000:rwX "+real {
		t.Fatalf("setfacl %v", calls[0])
	}
	for _, a := range []HostACL{
		{ServerID: "s1", Path: "garrysmod/escape"},
		{ServerID: "s1", Path: "garrysmod/missing"},
		{ServerID: "../s1", Path: "garrysmod/addons"},
	} {
		if _, err := h.Grant(context.Background(), 101000, []HostACL{a}); err == nil {
			t.Errorf("%+v was granted", a)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("refused grants ran setfacl: %v", calls)
	}
	if err := h.Revoke(context.Background(), 101000, []HostACL{{ServerID: "s1", Path: "garrysmod/addons"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls[1], " ") != "-R -P -x u:101000,d:u:101000 "+real {
		t.Fatalf("revoke %v", calls[1])
	}
}

// fakeGuest answers the commands the login flow runs.
type fakeGuest struct {
	cmds     []string
	loggedIn bool
	typed    string
}

func (f *fakeGuest) run(_ context.Context, argv ...string) ([]byte, error) {
	line := strings.Join(argv, " ")
	f.cmds = append(f.cmds, line)
	switch {
	case strings.Contains(line, "capture-pane"):
		return []byte("Opening browser to sign in…\nIf the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&client_id=x&state=y\nPaste code here if prompted >"), nil
	case strings.Contains(line, "send-keys") && strings.Contains(line, " -l "):
		f.typed = argv[len(argv)-1]
		f.loggedIn = true
	case strings.HasSuffix(line, "auth status --json"):
		if f.loggedIn {
			return []byte(`{"loggedIn": true, "authMethod": "claude.ai", "email": "op@example.com", "subscriptionType": "max"}`), nil
		}
		return []byte(`{"loggedIn": false}`), nil
	case strings.HasSuffix(line, "--version"):
		return []byte("2.1.308 (Claude Code)\n"), nil
	case strings.Contains(line, "is-active"):
		return []byte("active\n"), nil
	}
	return nil, nil
}

func TestGuestLogin(t *testing.T) {
	f := &fakeGuest{}
	g := Guest{Run: f.run, Sleep: func(time.Duration) {}}
	u, err := g.LoginStart(context.Background())
	if err != nil || !strings.HasPrefix(u, "https://claude.com/cai/oauth/authorize?") {
		t.Fatalf("login start %q %v", u, err)
	}
	if err := ValidCode("abc"); err == nil {
		t.Fatal("a short code was accepted")
	}
	if _, err := g.LoginCode(context.Background(), "abc'; rm -rf /"); err == nil {
		t.Fatal("a code with shell characters was typed")
	}
	st, err := g.LoginCode(context.Background(), "AbCdEf123456#xyz-789")
	if err != nil {
		t.Fatal(err)
	}
	if !st.LoggedIn || st.Account != "op@example.com" || st.Plan != "max" || f.typed != "AbCdEf123456#xyz-789" {
		t.Fatalf("status %+v typed %q", st, f.typed)
	}
	if !strings.Contains(strings.Join(f.cmds, "\n"), "systemctl restart ndl-claude.service") {
		t.Fatalf("Remote Control was not restarted: %v", f.cmds)
	}
	for _, c := range f.cmds {
		if strings.HasPrefix(c, "runuser") && !strings.HasPrefix(c, "runuser -u claude -- env HOME=/home/claude") {
			t.Errorf("command not run as the claude user: %s", c)
		}
	}
}

func TestConfigureRefusesBadSettings(t *testing.T) {
	g := Guest{Run: (&fakeGuest{}).run}
	if err := g.Configure(context.Background(), Request{Name: "x\"; rm"}); err == nil {
		t.Fatal("bad name accepted")
	}
	if err := g.Configure(context.Background(), Request{PermissionMode: "bypassPermissions"}); err == nil {
		t.Fatal("bypassPermissions accepted")
	}
	f := &fakeGuest{}
	if err := (Guest{Run: f.run}).Configure(context.Background(), Request{Name: "JvS dev", APIURL: "http://192.168.2.82:8080", APIToken: "ndlca_x"}); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 3 || !strings.Contains(f.cmds[0], "/etc/ndl-claude/env NDL_RC_NAME=\"JvS dev\"") {
		t.Fatalf("writes %v", f.cmds)
	}
}

func TestScriptsParse(t *testing.T) {
	for name, script := range map[string]string{"setup": setupScript, "write": writeFileScript} {
		if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("%s script: %v: %s", name, err, out)
		}
	}
}
