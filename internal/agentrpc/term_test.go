package agentrpc

import (
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/iojail"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

func TestTermArgvSystemContainerUsesTypedLXCAttach(t *testing.T) {
	id := "4de74354-52af-452c-a0d1-39c6cc695ed1"
	argv, err := termArgv(termRequest{
		TargetKind: iojail.TargetCT,
		TargetID:   id,
		LXCPath:    "/var/lib/ndl/runtime/lxc",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ctAttachArgv("/var/lib/ndl/runtime/lxc", id)
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v want %#v", argv, want)
	}
	if err := allowlisted(argv); err != nil {
		t.Fatal(err)
	}
	if argv[0] != lxc.BinLXCAttach {
		t.Fatalf("attach binary %q", argv[0])
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--clear-env") || !strings.Contains(joined, "LANG=C.UTF-8") {
		t.Fatal("attach must not leak host LANG")
	}
	if strings.Contains(joined, "/bin/login") {
		t.Fatal("login -f leaves a blank or dead PTY on unprivileged LXC")
	}
	if !strings.Contains(joined, "HOME=/root") || !strings.Contains(joined, "USER=root") {
		t.Fatal("root login environment is required")
	}
	if !strings.Contains(joined, "cd /root") || !strings.Contains(joined, "bash --login") {
		t.Fatal("must start a bash login shell in /root")
	}
	if argv[0] != "/usr/bin/lxc-attach" || strings.Contains(joined, "Host.Exec") {
		t.Fatal("typed attach must not become a host shell command")
	}
}

func TestTermArgvWorkloadAliasUsesTypedLXCAttach(t *testing.T) {
	argv, err := termArgv(termRequest{TargetKind: "workload", TargetID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "/usr/bin/lxc-attach" || argv[2] != "/var/lib/ndl/runtime/lxc" {
		t.Fatalf("workload terminal must use default LXC path: %#v", argv)
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "/bin/login") {
		t.Fatal("workload alias must not use login -f")
	}
	if !strings.Contains(joined, "bash --login") {
		t.Fatal("workload alias must open a root login shell")
	}
}

func TestAllowlistRejectsGenericExec(t *testing.T) {
	for _, argv := range [][]string{
		{"/usr/bin/nsenter", "-t", "1", "-m"},
		{"/usr/bin/newuidmap"},
		{"lxc-attach"},
	} {
		if err := allowlisted(argv); err == nil {
			t.Fatalf("must reject %#v", argv)
		}
	}
}
