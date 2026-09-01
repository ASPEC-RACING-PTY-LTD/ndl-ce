package journald

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAllowUnit(t *testing.T) {
	if _, err := AllowUnit("ndl-agent.service"); err != nil {
		t.Fatal(err)
	}
	if _, err := AllowUnit("bash -c evil"); err == nil {
		t.Fatal("must refuse arbitrary unit")
	}
	if _, err := AllowUnit("nodal-vm@not-a-uuid.service"); err == nil {
		t.Fatal("must refuse non-uuid instance")
	}
	id := uuid.NewString()
	if _, err := AllowUnit("nodal-vm@" + id + ".service"); err != nil {
		t.Fatal(err)
	}
	if _, err := AllowUnit("nodal-ct@" + id + ".service"); err != nil {
		t.Fatal(err)
	}
}

func TestArgvNeverShell(t *testing.T) {
	argv, err := Argv(Query{Unit: UnitAgent, Lines: 50})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "bash") || strings.Contains(joined, "-c") || strings.Contains(joined, "|") {
		t.Fatalf("shell-like argv: %v", argv)
	}
	if argv[0] != "/usr/bin/journalctl" {
		t.Fatalf("bin %s", argv[0])
	}
	foundU := false
	for i, a := range argv {
		if a == "-u" && i+1 < len(argv) && argv[i+1] == UnitAgent {
			foundU = true
		}
	}
	if !foundU {
		t.Fatalf("missing -u: %v", argv)
	}
	if _, err := Argv(Query{Unit: "syslog.service"}); err == nil {
		t.Fatal("must refuse host syslog")
	}
}

func TestSkipHostCmdsUnavailable(t *testing.T) {
	e := &Engine{SkipHostCmds: true}
	res, err := e.Read(context.Background(), Query{Unit: UnitControl, Lines: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUnavailable {
		t.Fatalf("status %s", res.Status)
	}
	if len(res.Lines) != 0 {
		t.Fatalf("invented lines %+v", res.Lines)
	}
}

func TestFixtureOutput(t *testing.T) {
	e := &Engine{
		Output: func(argv []string) ([]byte, error) {
			if strings.Contains(strings.Join(argv, " "), "bash") {
				t.Fatal(argv)
			}
			return []byte("2026-09-01T00:00:00Z ndl-agent[1]: hello\n"), nil
		},
	}
	res, err := e.Read(context.Background(), Query{Unit: UnitAgent, Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusAvailable || len(res.Lines) != 1 {
		t.Fatalf("%+v", res)
	}
}
