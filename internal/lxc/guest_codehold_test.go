package lxc

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/guestextras"
)

// chGuest fakes the few guest commands the CodeHold CLI step runs.
type chGuest struct {
	t       *testing.T
	files   map[string]string // path -> content; a "ch" content runs, "broken" does not
	server  string            // what the download returns
	curlErr string            // set to make the download fail
	noCurl  bool
	calls   []string
}

func newCHGuest(t *testing.T) *chGuest {
	return &chGuest{t: t, files: map[string]string{guestCACerts: "certs"}, server: "ch v2"}
}

func (g *chGuest) engine() *Engine {
	e := &Engine{DataDir: g.t.TempDir()}
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != BinLXCAttach {
			g.t.Fatalf("unexpected host command %s", name)
		}
		i := 0
		for i < len(args) && args[i] != "--" {
			i++
		}
		return g.exec(args[i+1:])
	}
	return e
}

func (g *chGuest) exec(argv []string) ([]byte, error) {
	g.calls = append(g.calls, strings.Join(argv, " "))
	switch argv[0] {
	case guestCHPath, guestCHTemp:
		c, ok := g.files[argv[0]]
		if !ok || c == "broken" {
			return nil, fmt.Errorf("exit status 126")
		}
		return []byte(c + "\nbuilt today\n"), nil
	case guestCurl:
		if g.noCurl {
			return nil, fmt.Errorf("not found")
		}
		if argv[1] == "--version" {
			return []byte("curl 8"), nil
		}
		if g.curlErr != "" {
			return []byte(g.curlErr + "\n"), fmt.Errorf("exit status 6")
		}
		for j, a := range argv {
			if a == "-o" {
				g.files[argv[j+1]] = g.server
			}
		}
		return nil, nil
	case "/usr/bin/test":
		if _, ok := g.files[argv[2]]; ok {
			return nil, nil
		}
		return nil, fmt.Errorf("exit status 1")
	case "/usr/bin/apt-get":
		if strings.Contains(strings.Join(argv, " "), " curl ca-certificates") {
			g.noCurl = false
		}
		return []byte("ok"), nil
	case "/bin/mkdir", "/bin/chmod":
		return nil, nil
	case "/usr/bin/cmp":
		if g.files[argv[2]] == g.files[argv[3]] {
			return nil, nil
		}
		return nil, fmt.Errorf("exit status 1")
	case "/bin/mv":
		g.files[argv[3]] = g.files[argv[2]]
		delete(g.files, argv[2])
		return nil, nil
	case "/bin/rm":
		delete(g.files, argv[2])
		return nil, nil
	case "/bin/mktemp":
		g.files["/tmp/.ndl-hdr.abc"] = ""
		return []byte("/tmp/.ndl-hdr.abc\n"), nil
	case "/bin/dd":
		return nil, nil
	case "/usr/bin/dpkg-query", "/usr/bin/docker", "/bin/docker":
		// The status scan after a run; nothing else is installed.
		return nil, fmt.Errorf("exit status 1")
	}
	g.t.Fatalf("unexpected guest command %v", argv)
	return nil, nil
}

func (g *chGuest) ran(prefix string) bool {
	for _, c := range g.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

const testPin = "debian/trixie/amd64/default"

var chSrc = &CodeholdSource{URL: "https://codehold.example.com"}

func chPkgs() []string { return guestextras.AvailabilityFor(testPin, guestextras.CodeholdCLI).Packages }

func TestCodeholdCLISkipsWhenChRuns(t *testing.T) {
	g := newCHGuest(t)
	g.files[guestCHPath] = "ch v1"
	got, err := g.engine().installGuestCodeholdCLI(context.Background(), "id", testPin, "debian", chPkgs(), extraInstall, chSrc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != ChangeCurrent || got.Version != "ch v1" {
		t.Fatalf("got %+v", got)
	}
	if len(g.calls) != 1 {
		t.Fatalf("a working ch must only be asked its version, ran %v", g.calls)
	}
}

func TestCodeholdCLIInstallsVerifiedBinary(t *testing.T) {
	g := newCHGuest(t)
	got, err := g.engine().installGuestCodeholdCLI(context.Background(), "id", testPin, "debian", chPkgs(), extraInstall, chSrc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != ChangeInstalled || got.Version != "ch v2" || g.files[guestCHPath] != "ch v2" {
		t.Fatalf("got %+v files %v", got, g.files)
	}
	if g.ran("/usr/bin/apt-get") {
		t.Fatal("curl and the CA bundle are present; apt-get must not run")
	}
	want := guestCurl + " -fsS -L --proto =https --proto-redir =https"
	if !g.ran(want) || !g.ran(guestCurl+" ") {
		t.Fatalf("curl must be limited to https: %v", g.calls)
	}
	for _, c := range g.calls {
		if strings.HasPrefix(c, guestCurl+" -fsS") && !strings.HasSuffix(c, "-o "+guestCHTemp+" https://codehold.example.com/downloads/ch-linux-amd64") {
			t.Fatalf("download must go to the temp file from the amd64 URL: %s", c)
		}
	}
	if _, ok := g.files[guestCHTemp]; ok {
		t.Fatal("the temp file must be gone")
	}
}

func TestCodeholdCLIInstallsCurlOnlyWhenMissing(t *testing.T) {
	g := newCHGuest(t)
	g.noCurl = true
	if _, err := g.engine().installGuestCodeholdCLI(context.Background(), "id", testPin, "debian", chPkgs(), extraInstall, chSrc); err != nil {
		t.Fatal(err)
	}
	if !g.ran("/usr/bin/apt-get -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold update") ||
		!g.ran("/usr/bin/apt-get -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install -y --no-install-recommends curl ca-certificates") {
		t.Fatalf("missing curl must be installed with apt-get: %v", g.calls)
	}
}

func TestCodeholdCLIUpdate(t *testing.T) {
	g := newCHGuest(t)
	g.files[guestCHPath] = "ch v1"
	got, err := g.engine().installGuestCodeholdCLI(context.Background(), "id", testPin, "debian", chPkgs(), extraUpdate, chSrc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != ChangeUpdated || g.files[guestCHPath] != "ch v2" {
		t.Fatalf("got %+v files %v", got, g.files)
	}

	// The same binary again is reported current and left in place.
	g.calls = nil
	got, err = g.engine().installGuestCodeholdCLI(context.Background(), "id", testPin, "debian", chPkgs(), extraUpdate, chSrc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != ChangeCurrent || g.ran("/bin/mv") {
		t.Fatalf("identical binary must not be replaced: %+v %v", got, g.calls)
	}
	if _, ok := g.files[guestCHTemp]; ok {
		t.Fatal("the temp file must be gone")
	}
}

func TestCodeholdCLIDownloadFailureKeepsCh(t *testing.T) {
	g := newCHGuest(t)
	g.files[guestCHPath] = "ch v1"
	g.curlErr = "curl: (6) Could not resolve host: codehold.example.com"
	_, err := g.engine().installGuestCodeholdCLI(context.Background(), "id", testPin, "debian", chPkgs(), extraUpdate, chSrc)
	if err == nil || !strings.Contains(err.Error(), "could not download ch from https://codehold.example.com/downloads/ch-linux-amd64") ||
		!strings.Contains(err.Error(), "Could not resolve host") {
		t.Fatalf("got %v", err)
	}
	if g.files[guestCHPath] != "ch v1" {
		t.Fatal("a failed download must leave ch alone")
	}
}

func TestCodeholdCLIBrokenDownloadKeepsCh(t *testing.T) {
	g := newCHGuest(t)
	g.files[guestCHPath] = "ch v1"
	g.server = "broken"
	_, err := g.engine().installGuestCodeholdCLI(context.Background(), "id", testPin, "debian", chPkgs(), extraReinstall, chSrc)
	if err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("got %v", err)
	}
	if g.files[guestCHPath] != "ch v1" || g.ran("/bin/mv") {
		t.Fatal("a binary that does not run must never replace ch")
	}
	if _, ok := g.files[guestCHTemp]; ok {
		t.Fatal("the temp file must be gone")
	}
}

func TestCodeholdCLIAccessHeadersStayOffTheCommandLine(t *testing.T) {
	g := newCHGuest(t)
	src := &CodeholdSource{URL: "https://codehold.example.com", AccessClientID: "id.access", AccessClientSecret: "s3cret-value"}
	if _, err := g.engine().installGuestCodeholdCLI(context.Background(), "id", testPin, "debian", chPkgs(), extraInstall, src); err != nil {
		t.Fatal(err)
	}
	for _, c := range g.calls {
		if strings.Contains(c, "s3cret-value") || strings.Contains(c, "id.access") {
			t.Fatalf("the Access token must not be on a command line: %s", c)
		}
	}
	if !g.ran("/bin/dd of=/tmp/.ndl-hdr.abc") || !g.ran(guestCurl+" -fsS") || !strings.Contains(strings.Join(g.calls, "|"), "-H @/tmp/.ndl-hdr.abc") {
		t.Fatalf("headers must go through the header file: %v", g.calls)
	}
	if _, ok := g.files["/tmp/.ndl-hdr.abc"]; ok {
		t.Fatal("the header file must be removed")
	}
}

func TestCHDownloadURL(t *testing.T) {
	got, err := CHDownloadURL("https://git.example.com/", "arm64")
	if err != nil || got != "https://git.example.com/downloads/ch-linux-arm64" {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range []string{"http://git.example.com", "git.example.com", "https://u:p@git.example.com", "https://", "ftp://x"} {
		if _, err := CHDownloadURL(bad, "amd64"); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
	if _, err := CHDownloadURL("https://git.example.com", "riscv64"); err == nil {
		t.Fatal("an unbuilt CPU type must be refused")
	}
}

func TestGuestExtrasReportsCodeholdFailureAsWarning(t *testing.T) {
	g := newCHGuest(t)
	g.curlErr = "curl: (7) Failed to connect"
	e := g.engine()
	if err := e.writeApplied(Spec{WorkloadID: "id", ImagePin: testPin}, true, ""); err != nil {
		t.Fatal(err)
	}
	res, err := e.guestExtras(context.Background(), LifecycleRequest{WorkloadID: "id", Extras: []string{guestextras.CodeholdCLI}, Codehold: chSrc}, extraInstall)
	if err != nil {
		t.Fatalf("a missing ch must not fail the container: %v", err)
	}
	if len(res.SetupWarnings) != 1 || res.SetupWarnings[0].Extra != guestextras.CodeholdCLI || !strings.Contains(res.SetupWarnings[0].Message, "Failed to connect") {
		t.Fatalf("warnings %+v", res.SetupWarnings)
	}
	if g.ran("/usr/bin/apt-get") {
		t.Fatal("the ch step must not refresh the package index when curl is present")
	}
}
