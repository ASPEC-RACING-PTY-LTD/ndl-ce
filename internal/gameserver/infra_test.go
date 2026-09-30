package gameserver

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAllocatePortsKeepsOffsetsAndBindsEnv(t *testing.T) {
	tmpl := Template{Name: "Valheim", DefaultPorts: []Port{
		{Name: "game", ContainerPort: 2456, Protocol: "udp", Primary: true, Env: "SERVER_PORT"},
		{Name: "query", ContainerPort: 2457, Protocol: "udp"},
	}}
	plan, err := AllocatePorts(tmpl, map[string]string{}, nil, false)
	if err != nil || plan.Delta != 0 || plan.Env["SERVER_PORT"] != "2456" {
		t.Fatalf("free node plan %+v %v", plan, err)
	}
	used := map[string]string{"2457/udp": "other"}
	plan, err = AllocatePorts(tmpl, used, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Delta != 2 || plan.Ports[0].HostPort != 2458 || plan.Ports[0].ContainerPort != 2458 || plan.Ports[1].HostPort != 2459 {
		t.Fatalf("shifted plan %+v", plan)
	}
	if plan.Env["SERVER_PORT"] != "2458" {
		t.Fatalf("env not updated %+v", plan.Env)
	}
}

func TestAllocatePortsNATsFixedPorts(t *testing.T) {
	tmpl := Template{Name: "Minecraft", DefaultPorts: []Port{{Name: "game", ContainerPort: 25565, Protocol: "tcp", Primary: true, Fixed: true}}}
	used := map[string]string{"25565/tcp": "survival"}
	plan, err := AllocatePorts(tmpl, used, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ports[0].ContainerPort != 25565 || plan.Ports[0].HostPort != 25566 {
		t.Fatalf("fixed port must stay in the container %+v", plan.Ports)
	}
	if len(plan.Notes) < 2 {
		t.Fatalf("expected notes explaining the move %v", plan.Notes)
	}
	if _, err := AllocatePorts(tmpl, used, nil, true); err == nil || !strings.Contains(err.Error(), "survival") {
		t.Fatalf("host network must refuse a taken fixed port, got %v", err)
	}
}

func TestAllocatePortsSharedEnvAcrossProtocols(t *testing.T) {
	tmpl := Template{Name: "CS:S", DefaultPorts: sourcePorts()}
	plan, err := AllocatePorts(tmpl, map[string]string{"27015/tcp": "tf2"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ports[0].HostPort != 27016 || plan.Ports[1].HostPort != 27016 || plan.Env["SERVER_PORT"] != "27016" {
		t.Fatalf("udp and tcp must move together %+v", plan)
	}
	probeBusy := func(port int, proto string) bool { return port != 27016 }
	plan, err = AllocatePorts(tmpl, map[string]string{"27015/tcp": "tf2"}, probeBusy, false)
	if err != nil || plan.Env["SERVER_PORT"] != "27017" {
		t.Fatalf("probe must be honoured %+v %v", plan, err)
	}
}

func TestCheckExplicitPorts(t *testing.T) {
	used := map[string]string{"7777/udp": "ark"}
	if err := CheckExplicitPorts([]Port{{ContainerPort: 7777, Protocol: "udp"}}, used, nil); err == nil || !strings.Contains(err.Error(), "ark") {
		t.Fatalf("conflict not reported: %v", err)
	}
	if err := CheckExplicitPorts([]Port{{ContainerPort: 7777, HostPort: 7780, Protocol: "udp"}}, used, nil); err != nil {
		t.Fatal(err)
	}
	if err := CheckExplicitPorts([]Port{{ContainerPort: 70000, Protocol: "udp"}}, nil, nil); err == nil {
		t.Fatal("out of range accepted")
	}
}

func withLoopbackDownloads(t *testing.T) {
	t.Helper()
	prev := downloadGuard
	downloadGuard = func(string) error { return nil }
	t.Cleanup(func() { downloadGuard = prev })
}

func TestFetchFileRetryResumesAndVerifies(t *testing.T) {
	withLoopbackDownloads(t)
	payload := bytes.Repeat([]byte("ndl-game-server-"), 4096)
	sum := sha256.Sum256(payload)
	var mu sync.Mutex
	calls := 0
	ranges := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		if rg := r.Header.Get("Range"); rg != "" {
			start, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			w.Header().Set("Content-Range", "bytes "+strconv.Itoa(start)+"-"+strconv.Itoa(len(payload)-1)+"/"+strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[start:])
			return
		}
		if n == 1 {
			// First attempt dies half way through.
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:len(payload)/2])
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
			return
		}
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "server.bin")
	err := fetchFileRetry(context.Background(), srv.URL+"/server.bin", dest, 1<<20, digest{Algo: "sha256", Hex: hex.EncodeToString(sum[:])}, 3)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch %d vs %d", len(got), len(payload))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ranges) < 2 || ranges[1] == "" {
		t.Fatalf("second attempt should resume with Range, saw %q", ranges)
	}
}

func TestFetchFileRetryRejectsChecksumMismatch(t *testing.T) {
	withLoopbackDownloads(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "x")
	err := fetchFileRetry(context.Background(), srv.URL, dest, 1<<20, digest{Algo: "sha256", Hex: strings.Repeat("0", 64)}, 2)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("mismatch not detected: %v", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Fatal("a file that failed verification must be removed")
	}
}

func TestFetchFileRetryStopsOn404(t *testing.T) {
	withLoopbackDownloads(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	defer srv.Close()
	start := time.Now()
	err := fetchFileRetry(context.Background(), srv.URL, filepath.Join(t.TempDir(), "x"), 1<<20, digest{}, 3)
	if err == nil || !strings.Contains(err.Error(), "404") || calls != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("404 must fail fast without retries: err=%v calls=%d", err, calls)
	}
}

func TestParseSHA256Sums(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	d, err := parseSHA256Sums(a+"  server-linux.tar.gz\n"+b+"  server-win.zip\n", "server-linux.tar.gz")
	if err != nil || d.Hex != a {
		t.Fatalf("got %+v %v", d, err)
	}
	d, err = parseSHA256Sums(b+"\n", "anything")
	if err != nil || d.Hex != b {
		t.Fatalf("single digest file %+v %v", d, err)
	}
	if _, err := parseSHA256Sums(a+" x\n"+b+" y\n", "z"); err == nil {
		t.Fatal("ambiguous file accepted")
	}
}

func TestExpandURLRejectsInjection(t *testing.T) {
	env := map[string]string{"VERSION": "1.2.3"}
	got, err := expandURL("https://example.org/dl/{{VERSION}}/server.tar.gz", env)
	if err != nil || got != "https://example.org/dl/1.2.3/server.tar.gz" {
		t.Fatalf("%s %v", got, err)
	}
	for _, bad := range []string{"../../etc", "1.0/../../x", "a b", "x?y=1", "@evil.com"} {
		if _, err := expandURL("https://example.org/{{VERSION}}", map[string]string{"VERSION": bad}); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestUnzipStripAndSafety(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"pkg-1.0/bin/server": "#!/bin/sh", "pkg-1.0/data/a.txt": "a"} {
		fw, _ := zw.Create(name)
		_, _ = fw.Write([]byte(body))
	}
	_ = zw.Close()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.zip")
	_ = os.WriteFile(src, buf.Bytes(), 0o640)
	dest := filepath.Join(dir, "out")
	_ = os.MkdirAll(dest, 0o750)
	if err := unzipStrip(src, dest, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "server")); err != nil {
		t.Fatalf("strip failed: %v", err)
	}
	buf.Reset()
	zw = zip.NewWriter(&buf)
	fw, _ := zw.Create("../../escape.txt")
	_, _ = fw.Write([]byte("x"))
	_ = zw.Close()
	_ = os.WriteFile(src, buf.Bytes(), 0o640)
	if err := unzipStrip(src, dest, 0); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestValidateDownloadTemplate(t *testing.T) {
	good := Template{InstallBuiltin: "github-release", Variables: []Variable{envText("Version", "VERSION", "v", "latest", true)},
		Install: &InstallSpec{Downloads: []Download{{Repo: "owner/name", Asset: `^server-linux\.tar\.gz$`}}}}
	if err := validateDownloadTemplate(good); err != nil {
		t.Fatal(err)
	}
	bad := []InstallSpec{
		{Downloads: []Download{{Repo: "not a repo", Asset: "x"}}},
		{Downloads: []Download{{Repo: "o/n", Asset: "("}}},
		{Downloads: []Download{{URL: "http://insecure.example/x.zip"}}},
		{Downloads: []Download{{URL: "https://example.org/{{UNDECLARED}}.zip"}}},
		{Downloads: []Download{{Repo: "o/n", Asset: "x", Executables: []string{"../x"}}}},
	}
	for i, spec := range bad {
		spec := spec
		tmpl := good
		tmpl.InstallBuiltin = "download"
		tmpl.Install = &spec
		if err := validateDownloadTemplate(tmpl); err == nil {
			t.Fatalf("bad spec %d accepted", i)
		}
	}
}

type recordedRun struct {
	mu    sync.Mutex
	calls [][]string
	reply func(args []string) ([]byte, error)
}

func (r *recordedRun) runner() Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		r.mu.Lock()
		r.calls = append(r.calls, append([]string{name}, args...))
		r.mu.Unlock()
		if r.reply != nil {
			return r.reply(args)
		}
		return []byte("ok"), nil
	}
}

func (r *recordedRun) find(verb string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]string
	for _, c := range r.calls {
		if len(c) > 1 && c[1] == verb {
			out = append(out, c)
		}
	}
	return out
}

func TestStartKeepsStdinOpenAndSetsHome(t *testing.T) {
	rec := &recordedRun{}
	rt := NewRuntime(t.TempDir())
	rt.Run = rec.runner()
	if _, err := rt.Start(context.Background(), RunSpec{ID: "s1", Image: "debian:bookworm-slim", Startup: "./server", WorkDir: "/home/container", Env: map[string]string{"B": "2", "A": "1"}}); err != nil {
		t.Fatal(err)
	}
	runs := rec.find("run")
	if len(runs) != 1 {
		t.Fatalf("runs %v", rec.calls)
	}
	joined := strings.Join(runs[0], " ")
	if !strings.Contains(joined, "run -d -i ") || !strings.Contains(joined, "HOME=/home/container") {
		t.Fatalf("start argv %s", joined)
	}
	if strings.Index(joined, "A=1") > strings.Index(joined, "B=2") {
		t.Fatal("env should be passed in a stable order")
	}
}

func TestStopGracefulUsesTemplateStop(t *testing.T) {
	rec := &recordedRun{reply: func(args []string) ([]byte, error) {
		if args[0] == "inspect" {
			return []byte("true"), nil
		}
		return []byte("ok"), nil
	}}
	rt := NewRuntime(t.TempDir())
	rt.Run = rec.runner()
	if err := rt.StopGraceful(context.Background(), "s1", "save-and-quit", time.Second); err != nil {
		t.Fatal(err)
	}
	execs := rec.find("exec")
	if len(execs) != 1 || !strings.Contains(strings.Join(execs[0], " "), "save-and-quit") {
		t.Fatalf("stop command not sent: %v", rec.calls)
	}
	if len(rec.find("wait")) != 1 || len(rec.find("stop")) != 1 {
		t.Fatalf("expected wait then docker stop: %v", rec.calls)
	}
	rec2 := &recordedRun{reply: rec.reply}
	rt.Run = rec2.runner()
	_ = rt.StopGraceful(context.Background(), "s1", "^C", time.Second)
	kills := rec2.find("kill")
	if len(kills) != 1 || !strings.Contains(strings.Join(kills[0], " "), "SIGINT") {
		t.Fatalf("^C must send SIGINT: %v", rec2.calls)
	}
}

func TestEnsureRuntimeImageBuildsOnceAndValidates(t *testing.T) {
	built := false
	rec := &recordedRun{}
	rec.reply = func(args []string) ([]byte, error) {
		if args[0] == "image" && !built {
			return nil, os.ErrNotExist
		}
		if args[0] == "commit" {
			built = true
		}
		return []byte("ok"), nil
	}
	rt := NewRuntime(t.TempDir())
	rt.Run = rec.runner()
	tag, err := rt.EnsureRuntimeImage(context.Background(), "steamcmd/steamcmd:debian", []string{"libsdl2-2.0-0:i386", "lib32gcc-s1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tag, "ndl-gs-runtime:") {
		t.Fatalf("tag %s", tag)
	}
	run := strings.Join(rec.find("run")[0], " ")
	if !strings.Contains(run, "dpkg --add-architecture i386") || !strings.Contains(run, "lib32gcc-s1 libsdl2-2.0-0:i386") {
		t.Fatalf("build script %s", run)
	}
	if again, err := rt.EnsureRuntimeImage(context.Background(), "steamcmd/steamcmd:debian", []string{"lib32gcc-s1", "libsdl2-2.0-0:i386"}); err != nil || again != tag {
		t.Fatalf("second call should reuse %s: %s %v", tag, again, err)
	}
	if len(rec.find("commit")) != 1 {
		t.Fatal("image must be built once")
	}
	if _, err := rt.EnsureRuntimeImage(context.Background(), "debian:bookworm-slim", []string{"curl; rm -rf /"}); err == nil {
		t.Fatal("shell injection in a package name accepted")
	}
}

func TestSteamcmdPlanAppConfigBetaAndRetries(t *testing.T) {
	tmpl := goldsrcTemplate(SourceGame{ID: "ndl-test-cs16", Name: "CS", GameTitle: "Counter-Strike", AppID: "90", GameDir: "cstrike", Mod: "cstrike", Map: "de_dust2", MaxPlayers: 16, Category: "shooter"})
	script, image := installPlan(tmpl)
	if image != "steamcmd/steamcmd:debian" {
		t.Fatal(image)
	}
	for _, want := range []string{"+app_set_config 90 mod cstrike", "for attempt in 1 2 3", "sdk${arch}/steamclient.so"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script lacks %q", want)
		}
	}
	tmpl.Install.Beta = "steam_legacy"
	script, _ = installPlan(tmpl)
	if !strings.Contains(script, `BETA="${STEAM_BETA:-steam_legacy}"`) {
		t.Fatal("beta default not applied")
	}
	win := tmpl
	win.Install = &InstallSpec{Platform: "windows"}
	if err := validateSteamTemplate(win); err == nil {
		t.Fatal("windows platform must be rejected")
	}
}

func TestInstallRequirementsBlockAnonymousOwnerApps(t *testing.T) {
	tmpl, ok := templateByID("ndl-dayz")
	if !ok {
		t.Fatal("dayz missing")
	}
	rt := NewRuntime(t.TempDir())
	rt.Run = (&recordedRun{}).runner()
	err := rt.Install(context.Background(), Server{ID: "d1", Env: mergeEnv(tmpl, nil)}, tmpl)
	if err == nil || !strings.Contains(err.Error(), "STEAM_USER") {
		t.Fatalf("anonymous dayz install should be refused: %v", err)
	}
	err = checkInstallRequirements(tmpl, map[string]string{"STEAM_USER": "anonymous"})
	if err == nil {
		t.Fatal("anonymous must not satisfy an ownership requirement")
	}
	if err := checkInstallRequirements(tmpl, map[string]string{"STEAM_USER": "operator"}); err != nil {
		t.Fatal(err)
	}
}

func TestStartRequirements(t *testing.T) {
	paper, _ := templateByID("ndl-minecraft-paper")
	if err := CheckStartRequirements(paper, map[string]string{"EULA": "false"}); err == nil {
		t.Fatal("EULA=false must block start")
	}
	if err := CheckStartRequirements(paper, map[string]string{"EULA": "true"}); err != nil {
		t.Fatal(err)
	}
	fivem, _ := templateByID("ndl-fivem")
	if err := CheckStartRequirements(fivem, map[string]string{}); err == nil || !strings.Contains(err.Error(), "fivem") {
		t.Fatalf("fivem key: %v", err)
	}
}

func TestRuntimeImageInstallUsesSelectedJava(t *testing.T) {
	rec := &recordedRun{}
	rt := NewRuntime(t.TempDir())
	rt.Run = rec.runner()
	tmpl := minecraftJava(Template{ID: "ndl-test-forge", Name: "Forge", Summary: "x", Implementation: "forge", InstallBuiltin: "minecraft-loader",
		Install: &InstallSpec{Project: "forge"}, Startup: moddedJavaStartup,
		Variables: []Variable{envText("Minecraft version", "MC_VERSION", "v", "1.20.1", true)}}, false)
	env := mergeEnv(tmpl, map[string]string{"EULA": "true"})
	if err := rt.Install(context.Background(), Server{ID: "f1", Env: env, Image: "eclipse-temurin:17-jre"}, tmpl); err != nil {
		t.Fatal(err)
	}
	run := strings.Join(rec.find("run")[0], " ")
	if !strings.Contains(run, "eclipse-temurin:17-jre /mnt/server/.ndl-install.sh") {
		t.Fatalf("loader must run in the selected Java image: %s", run)
	}
	script, _ := os.ReadFile(filepath.Join(rt.DataDir("f1"), ".ndl-install.sh"))
	if !strings.Contains(string(script), `LOADER="forge"`) || !strings.Contains(string(script), "ndl-java-args.txt") {
		t.Fatal("loader script not specialised")
	}
}

func TestCatalogueExtendedSearchAndHidden(t *testing.T) {
	items := BuiltinCatalogue()
	if catalogueHas(items, "ndl-conan") {
		t.Fatal("hidden template leaked into the catalogue")
	}
	if _, ok := templateByID("ndl-conan"); !ok {
		t.Fatal("hidden template must still resolve for existing servers")
	}
	if !catalogueHas(MatchCatalogue(items, "unity"), "ndl-valheim") {
		t.Fatal("engine search")
	}
	if !catalogueHas(FilterCatalogue(items, "arm64"), "ndl-minecraft-paper") || catalogueHas(FilterCatalogue(items, "arm64"), "ndl-rust") {
		t.Fatal("arm64 filter")
	}
	free := FilterCatalogue(items, "no-credentials")
	if catalogueHas(free, "ndl-dayz") || catalogueHas(free, "ndl-fivem") || !catalogueHas(free, "ndl-valheim") {
		t.Fatalf("no-credentials filter %v", idsOf(free))
	}
	if !catalogueHas(FilterCatalogue(items, "survival"), "ndl-rust") {
		t.Fatal("category filter")
	}
	for _, item := range items {
		if item.GameTitle == "" || item.Category == "" || item.InstallMethod == "" || item.Verification == "" || len(item.Ports) == 0 {
			t.Fatalf("catalogue item incomplete %+v", item)
		}
	}
}

func TestJavaForMinecraft(t *testing.T) {
	cases := map[string]int{"1.12.2": 8, "1.16.5": 8, "1.17.1": 16, "1.18.2": 17, "1.20.4": 17, "1.20.5": 21, "1.21.1": 21, "26.1": 25, "26.1.2": 25, "snapshot": 0}
	for mc, want := range cases {
		if got := javaForMinecraft(mc); got != want {
			t.Fatalf("%s: got %d want %d", mc, got, want)
		}
	}
	if javaOfImage("eclipse-temurin:17-jre") != 17 || javaOfImage("eclipse-temurin:25-jdk") != 25 || javaOfImage("debian:bookworm-slim") != 0 {
		t.Fatal("javaOfImage")
	}
}

func TestHumanErrorForNewInstallFailures(t *testing.T) {
	cases := map[string]string{
		"STEAM_USER is required to install: Steam account that owns DayZ": "owns the game",
		"sha256 checksum mismatch: expected a, got b":                     "checksum",
		"owner/repo release v1 has no asset matching ^x$":                 "release",
		"modpack x targets Minecraft 26.1, which needs Java 25":           "Java",
		"runtime dependencies failed: exit status 100":                    "libraries",
	}
	for raw, want := range cases {
		if got := HumanError(raw); !strings.Contains(got, want) {
			t.Fatalf("%q -> %q, want mention of %q", raw, got, want)
		}
	}
}

func TestPreserveAndRestoreKeptFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "cfg"), 0o750)
	_ = os.WriteFile(filepath.Join(dir, "cfg", "server.json"), []byte("operator"), 0o640)
	kept, err := preserveFiles(dir, []string{"cfg/server.json", "missing.txt"})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "cfg", "server.json"), []byte("shipped default"), 0o640)
	if err := restoreFiles(dir, kept); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "cfg", "server.json"))
	if string(got) != "operator" {
		t.Fatalf("kept file overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.txt")); err == nil {
		t.Fatal("a file that did not exist must not be created")
	}
	if _, err := preserveFiles(dir, []string{"../escape"}); err == nil {
		t.Fatal("unsafe keep path accepted")
	}
}

func TestSteamAddonScriptRunsAfterOverlay(t *testing.T) {
	tmpl := steamServer(Template{ID: "ndl-test-addon", InstallScript: "echo patch-after-overlay",
		Install: &InstallSpec{Downloads: []Download{{Repo: "o/n", Asset: "x"}}}}, "258550", "x")
	script, _ := installPlan(tmpl)
	if strings.Contains(script, "patch-after-overlay") {
		t.Fatal("with add-on downloads the template script must not run inside the SteamCMD pass")
	}
	tmpl.Install = nil
	script, _ = installPlan(tmpl)
	if !strings.Contains(script, "patch-after-overlay") {
		t.Fatal("without add-ons the script still runs after app_update")
	}
}
