package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type listOnly struct{ items []listItem }

func (l listOnly) ping(context.Context) (string, error)               { return "28", nil }
func (l listOnly) listContainers(context.Context) ([]listItem, error) { return l.items, nil }
func (l listOnly) inspect(context.Context, string) (inspectJSON, error) {
	return inspectJSON{}, errors.New("no")
}
func (l listOnly) stats(context.Context, string) (statsJSON, error)  { return statsJSON{}, nil }
func (l listOnly) start(context.Context, string) error               { return nil }
func (l listOnly) stop(context.Context, string) error                { return nil }
func (l listOnly) restart(context.Context, string) error             { return nil }
func (l listOnly) logs(context.Context, string, int) (string, error) { return "", nil }
func (l listOnly) pull(context.Context, string) error                { return nil }
func (l listOnly) recreate(context.Context, string) error            { return nil }
func (l listOnly) remove(context.Context, string) error              { return nil }
func (l listOnly) eventsSince(context.Context, time.Time, time.Time) ([]dockerEvent, error) {
	return nil, nil
}

func fakeELF() []byte {
	return append([]byte("\x7fELF"), bytes.Repeat([]byte{1}, 2<<20)...)
}

func TestCodeholdDetectFindsServerWithRunnerBinaries(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e := &Engine{
		HostSockets: []string{sock},
		ClientFor: func(string) engineAPI {
			return listOnly{items: []listItem{
				{ID: "srv", Names: []string{"/codehold-codehold-1"}, Image: "codehold-codehold", State: "running",
					Labels: map[string]string{"com.docker.compose.service": "codehold"}, Ports: []apiPort{{PrivatePort: 80, PublicPort: 8085, Type: "tcp"}}},
				{ID: "ctl", Names: []string{"/codehold-controller-1"}, Image: "codehold-runner:local", State: "running"},
				{ID: "db", Names: []string{"/codehold-postgres-1"}, Image: "postgres:16", State: "running"},
			}}
		},
		ArchiveFile: func(_ context.Context, _, id, p string, _ int64) ([]byte, error) {
			if id == "srv" && p == CodeholdDownloadDir+"/codehold-runner-linux-amd64" {
				return []byte("x"), nil
			}
			return nil, errors.New("missing")
		},
	}
	res := e.codeholdDetectOn(context.Background(), MachineHint{ID: HostMachineID, Name: "Host"}, sock)
	if len(res) != 1 || res[0].ContainerID != "srv" || res[0].HTTPPort != 8085 || !res[0].HasRunner {
		t.Fatalf("%+v", res)
	}
}

func TestCodeholdInstallTokenAndStatus(t *testing.T) {
	root := t.TempDir()
	var cmds []string
	active := "active"
	bin := fakeELF()
	sum := sha256.Sum256(bin)
	e := &Engine{
		HostRoot: root,
		HostRun: func(_ context.Context, name string, args ...string) ([]byte, error) {
			cmd := strings.Join(append([]string{name}, args...), " ")
			cmds = append(cmds, cmd)
			if strings.Contains(cmd, "ActiveState") {
				return []byte(active + "\n78\n"), nil
			}
			return nil, nil
		},
		Download: func(_ context.Context, u string) ([]byte, error) {
			if u != "https://git.example.com/downloads/codehold-runner-linux-amd64" {
				return nil, errors.New("wrong url " + u)
			}
			return bin, nil
		},
	}
	ctx := context.Background()
	req := CodeholdRequest{Action: CodeholdInstall, MachineID: HostMachineID, Source: CodeholdSource{URL: "https://git.example.com/"}, RunnerURL: "http://192.168.2.196:8085", CFID: "id", CFSecret: "secret"}
	// A wrong checksum is refused before anything is written.
	req.Source.SHA256 = strings.Repeat("0", 64)
	if _, err := e.Codehold(ctx, req); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("checksum: %v", err)
	}
	runnerBinaries.Range(func(k, _ any) bool { runnerBinaries.Delete(k); return true })
	req.Source.SHA256 = hex.EncodeToString(sum[:])
	res, err := e.Codehold(ctx, req)
	if err != nil || !res.Installed {
		t.Fatalf("%+v %v", res, err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, codeholdBinary)); !bytes.Equal(got, bin) {
		t.Fatal("binary not installed")
	}
	unit, _ := os.ReadFile(filepath.Join(root, codeholdUnitPath))
	if !strings.Contains(string(unit), "--registration-file /etc/codehold-runner/registration.token --url http://192.168.2.196:8085") {
		t.Fatalf("unit:\n%s", unit)
	}
	if strings.Contains(string(unit), "secret") {
		t.Fatal("the Cloudflare secret must not be in the unit file")
	}
	envInfo, _ := os.Stat(filepath.Join(root, codeholdEnvFile))
	if envInfo == nil || envInfo.Mode().Perm() != 0o600 {
		t.Fatalf("env file mode %v", envInfo)
	}
	// The token goes into a root-only file, never a command line.
	if _, err := e.Codehold(ctx, CodeholdRequest{Action: CodeholdToken, MachineID: HostMachineID, Token: "chreg_abcdef123456"}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(root, codeholdRegFile))
	if st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("token file %v", st)
	}
	for _, c := range cmds {
		if strings.Contains(c, "chreg_") || strings.Contains(c, "secret") {
			t.Fatalf("secret on a command line: %s", c)
		}
	}
	if _, err := e.Codehold(ctx, CodeholdRequest{Action: CodeholdToken, MachineID: HostMachineID, Token: "bad token\n"}); err == nil {
		t.Fatal("a token with spaces was accepted")
	}
	// Exit status 78 or the agent's marker means it needs a new token.
	st2, err := e.Codehold(ctx, CodeholdRequest{Action: CodeholdStatus, MachineID: HostMachineID})
	if err != nil || !st2.Installed || !st2.Active || !st2.AuthFailed || !st2.TokenWait {
		t.Fatalf("status %+v %v", st2, err)
	}
}

func TestCodeholdRefusesUnsafeSources(t *testing.T) {
	e := &Engine{}
	if _, err := e.codeholdBinary(context.Background(), CodeholdSource{URL: "http://git.example.com/"}, "amd64"); err == nil {
		t.Fatal("plain http accepted")
	}
	if _, err := e.codeholdBinary(context.Background(), CodeholdSource{URL: "https://x/"}, "mips"); err == nil {
		t.Fatal("odd arch accepted")
	}
	if _, err := e.Codehold(context.Background(), CodeholdRequest{Action: CodeholdInstall, RunnerURL: "http://x; rm -rf /"}); err == nil {
		t.Fatal("bad runner url accepted")
	}
}
