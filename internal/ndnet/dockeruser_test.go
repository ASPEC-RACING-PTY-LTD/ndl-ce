package ndnet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fakeIPTables models one DOCKER-USER chain per binary.
type fakeIPTables struct {
	chains  map[string][]string // bin -> "-A DOCKER-USER ..." lines
	forward string
	calls   []string
}

func (f *fakeIPTables) engine(t *testing.T) *Engine {
	t.Helper()
	root := t.TempDir()
	return &Engine{
		Root:     root,
		StateDir: filepath.Join(root, "var/lib/ndl/net"),
		Run: func(_ context.Context, name string, args ...string) error {
			f.calls = append(f.calls, name+" "+strings.Join(args, " "))
			lines, ok := f.chains[name]
			if !ok {
				return errors.New("no chain")
			}
			op, rule := args[2], strings.Join(args[3:], " ")
			switch op {
			case "-C":
				for _, l := range lines {
					if l == "-A "+rule {
						return nil
					}
				}
				return errors.New("bad rule")
			case "-I":
				rule = strings.Replace(rule, DockerUserChain+" 1 ", DockerUserChain+" ", 1)
				f.chains[name] = append([]string{"-A " + rule}, lines...)
			case "-D":
				for i, l := range lines {
					if l == "-A "+rule {
						f.chains[name] = append(lines[:i:i], lines[i+1:]...)
						return nil
					}
				}
				return errors.New("no rule")
			}
			return nil
		},
		Output: func(_ context.Context, name string, args ...string) (string, error) {
			if args[len(args)-1] == "FORWARD" {
				return f.forward, nil
			}
			lines, ok := f.chains[name]
			if !ok {
				return "", errors.New("no chain")
			}
			return "-N DOCKER-USER\n" + strings.Join(lines, "\n") + "\n", nil
		},
	}
}

func addBridge(t *testing.T, e *Engine, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(e.Root, "sys/class/net", name, "bridge"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureForwardAcceptInsertsOnceForLANBridge(t *testing.T) {
	f := &fakeIPTables{chains: map[string][]string{IPTablesBin: {"-A DOCKER-USER -j RETURN"}, IP6TablesBin: {}}}
	e := f.engine(t)
	addBridge(t, e, "ndl685ff937")
	// Not a bridge, and not an ndl name: both ignored.
	_ = os.MkdirAll(filepath.Join(e.Root, "sys/class/net", "ndl12345678"), 0o755)
	addBridge(t, e, "docker0")

	st, err := e.EnsureForwardAccept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Inserted != 2 || len(st.Bridges) != 1 || st.Bridges[0] != "ndl685ff937" {
		t.Fatalf("%+v", st)
	}
	want := "-A DOCKER-USER -i ndl685ff937 -o ndl685ff937 -m comment --comment ndl-ce-bridge -j ACCEPT"
	if f.chains[IPTablesBin][0] != want {
		t.Fatalf("rule must be first in DOCKER-USER: %q", f.chains[IPTablesBin])
	}
	st, err = e.EnsureForwardAccept(context.Background())
	if err != nil || st.Inserted != 0 {
		t.Fatalf("second pass must be a no-op: %+v %v", st, err)
	}
}

func TestEnsureForwardAcceptNATBridgeAndPrune(t *testing.T) {
	f := &fakeIPTables{chains: map[string][]string{IPTablesBin: {
		"-A DOCKER-USER -i ndldeadbeef -o ndldeadbeef -m comment --comment ndl-ce-bridge -j ACCEPT",
		"-A DOCKER-USER -i docker0 -j ACCEPT",
	}}}
	e := f.engine(t)
	id := uuid.NewString()
	br, _ := BridgeName(id)
	addBridge(t, e, br)
	if err := os.MkdirAll(filepath.Join(e.StateDir, "nft"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(e.StateDir, "nft", id+".nft"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(e.StateDir, "nft", id+".destroy.nft"), []byte("x"), 0o600)

	st, err := e.EnsureForwardAccept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Inserted != 3 || st.Removed != 1 {
		t.Fatalf("%+v %q", st, f.chains[IPTablesBin])
	}
	all := strings.Join(f.chains[IPTablesBin], "\n")
	if strings.Contains(all, "ndldeadbeef") {
		t.Fatal("rules for a deleted bridge must be pruned")
	}
	if !strings.Contains(all, "-i docker0 -j ACCEPT") {
		t.Fatal("foreign rules must stay")
	}
	if !strings.Contains(all, "! -i "+br+" -o "+br+" -m conntrack --ctstate RELATED,ESTABLISHED") {
		t.Fatalf("nat return path missing: %s", all)
	}
}

func TestEnsureForwardAcceptReportsBlockedWithoutDockerUser(t *testing.T) {
	f := &fakeIPTables{chains: map[string][]string{}, forward: "-P FORWARD DROP\n"}
	e := f.engine(t)
	addBridge(t, e, "ndl685ff937")
	dir := filepath.Join(e.Root, "proc/sys/net/bridge")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bridge-nf-call-iptables"), []byte("1\n"), 0o644)
	st, err := e.EnsureForwardAccept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Blocked || st.Inserted != 0 {
		t.Fatalf("%+v", st)
	}
}
