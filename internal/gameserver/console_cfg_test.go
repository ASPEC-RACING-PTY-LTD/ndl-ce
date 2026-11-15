package gameserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The JvS production server.cfg: comments, a URL holding // inside quotes,
// an exec line and sv_allowdownload set twice.
const prodServerCfg = `// TheSenate JvS
hostname "AU | TheSenate JvS | thesenate.xyz"
sv_lan 0
sv_region 5                 // Australia
sv_location "au"
sv_password ""
sv_allowupload 0
sv_allowdownload 0          // content comes from the workshop collection
sv_downloadurl "https://thesenate.xyz/fastdl"
sv_allowdownload 1
sv_loadingurl "https://thesenate.xyz/loading/jvs?steamid=%s&map=%m"
net_maxfilesize 64

// RCON off on prod: an empty password disables it.
rcon_password ""

log on
lua_log_sv 1

exec tsweb.cfg
ts_debug 0
`

func TestConsoleCfgReadsAndKeepsAProdFile(t *testing.T) {
	f := ParseConsoleCfg(prodServerCfg)
	if f.Bytes() != prodServerCfg {
		t.Fatalf("an unchanged file must round-trip exactly:\n%s", f.Bytes())
	}
	for key, want := range map[string]string{
		"hostname": "AU | TheSenate JvS | thesenate.xyz", "sv_region": "5", "sv_allowdownload": "1",
		"sv_downloadurl": "https://thesenate.xyz/fastdl", "sv_loadingurl": "https://thesenate.xyz/loading/jvs?steamid=%s&map=%m",
		"rcon_password": "", "log": "on", "SV_LAN": "0",
	} {
		if got, ok := f.Get(key); !ok || got != want {
			t.Fatalf("%s = %q (%v), want %q", key, got, ok, want)
		}
	}
	f.Set("sv_region", "3", false)
	f.Set("sv_allowdownload", "0", false)
	f.Set("sv_password", "letmein", true)
	f.Set("sv_hibernate_think", "1", false)
	out := f.Bytes()
	for _, want := range []string{
		"sv_region 3                 // Australia\n", // comment and spacing kept
		"sv_allowdownload 0          // content comes from the workshop collection\n" + `sv_downloadurl "https://thesenate.xyz/fastdl"` + "\nsv_allowdownload 0\n", // the line that wins changes
		`sv_password "letmein"`, "exec tsweb.cfg\nts_debug 0\nsv_hibernate_think 1\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestConsoleCfgSettings(t *testing.T) {
	tmpl := gmodTemplate()
	files := map[string]string{gmodServerCfg: prodServerCfg}
	got := readFriendly(tmpl.FriendlyConfig, tmpl.ConfigFiles, files, map[string]string{"SERVER_NAME": "ignored"})
	if got["hostname"] != "AU | TheSenate JvS | thesenate.xyz" || got["sv_allowdownload"] != "true" || got["log"] != "true" || got["sv_lan"] != "false" || got["sv_region"] != "5" {
		t.Fatalf("read %v", got)
	}
	next, env, err := applyFriendly(tmpl.FriendlyConfig, tmpl.ConfigFiles, map[string]string{"log": "false", "sv_lan": "true", "hostname": "Dev", "net_maxfilesize": "32"}, files, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := next[gmodServerCfg]
	for _, want := range []string{"log off\n", "sv_lan 1\n", `hostname "Dev"`, "net_maxfilesize 32\n"} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("missing %q in:\n%s", want, cfg)
		}
	}
	if env["SERVER_NAME"] != "Dev" {
		t.Fatalf("the server name must reach the startup line too: %v", env)
	}
	for _, bad := range []string{"x\"; rcon_password hunter2", "x\nrcon_password hunter2"} {
		if _, _, err := applyFriendly(tmpl.FriendlyConfig, tmpl.ConfigFiles, map[string]string{"sv_loadingurl": bad}, files, nil); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}

// FiveM's server.cfg is a console script too; it used to get key=value lines
// FiveM ignores.
func TestFiveMSettingsWriteConsoleLines(t *testing.T) {
	tmpl := fivemTemplate()
	next, _, err := applyFriendly(tmpl.FriendlyConfig, tmpl.ConfigFiles, map[string]string{"sv_hostname": "My City", "sv_maxclients": "48"}, map[string]string{"server.cfg": "endpoint_add_tcp \"0.0.0.0:30120\"\n"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := next["server.cfg"]; !strings.Contains(cfg, `sv_hostname "My City"`) || !strings.Contains(cfg, "sv_maxclients 48") || strings.Contains(cfg, "=") {
		t.Fatalf("FiveM cfg:\n%s", cfg)
	}
}

// Properties files refuse line breaks too: a value could otherwise add a
// line such as enable-rcon=true.
func TestPropertiesSettingsRefuseLineBreaks(t *testing.T) {
	s := []Setting{{ID: "motd", Label: "Message", Kind: "text", File: "server.properties", Key: "motd"}}
	if _, _, err := applyFriendly(s, []ConfigFile{{Path: "server.properties"}}, map[string]string{"motd": "hi\nenable-rcon=true"}, map[string]string{}, nil); err == nil {
		t.Fatal("a line break must be refused")
	}
}

func TestSeedFilesReplaceOnlyBlankFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "garrysmod", "cfg")
	_ = os.MkdirAll(cfg, 0o755)
	_ = os.WriteFile(filepath.Join(cfg, "server.cfg"), []byte("\r\n\r\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cfg, "kept.cfg"), []byte("hostname \"mine\"\n"), 0o644)
	outside := filepath.Join(t.TempDir(), "victim.cfg")
	_ = os.Symlink(outside, filepath.Join(cfg, "link.cfg"))
	err := writeSeedFiles(dir, []SeedFile{
		{Path: gmodServerCfg, Body: gmodServerCfgSeed},
		{Path: "garrysmod/cfg/kept.cfg", Body: "x"},
		{Path: "garrysmod/cfg/link.cfg", Body: "x"},
		{Path: "garrysmod/cfg/new.cfg", Body: "new"},
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	if read(filepath.Join(cfg, "server.cfg")) != gmodServerCfgSeed || read(filepath.Join(cfg, "kept.cfg")) != "hostname \"mine\"\n" || read(filepath.Join(cfg, "new.cfg")) != "new" {
		t.Fatal("seed must fill blank and missing files and keep written ones")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("a seed must never be written through a link")
	}
}
