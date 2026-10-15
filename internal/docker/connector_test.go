package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testTunnel = "a0a54058-1111-4222-8333-444455556666"

func connectorEngine(t *testing.T, withApt, withBinary bool) (*Engine, string, *[]string) {
	t.Helper()
	root := t.TempDir()
	if withApt {
		_ = os.MkdirAll(filepath.Join(root, "usr/bin"), 0o755)
		_ = os.WriteFile(filepath.Join(root, binAptGet), []byte("#!/bin/sh\n"), 0o755)
	}
	if withBinary {
		_ = os.MkdirAll(filepath.Join(root, "usr/bin"), 0o755)
		_ = os.WriteFile(filepath.Join(root, "usr/bin/cloudflared"), []byte("bin"), 0o755)
	}
	var ran []string
	e := &Engine{
		HostRoot:     root,
		FetchKeyring: func(context.Context) ([]byte, error) { return []byte("KEYRING"), nil },
		HostRun: func(_ context.Context, name string, args ...string) ([]byte, error) {
			line := name + " " + strings.Join(args, " ")
			ran = append(ran, line)
			if name == binAptGet && len(args) > 0 && args[0] == "install" {
				_ = os.WriteFile(filepath.Join(root, "usr/bin/cloudflared"), []byte("bin"), 0o755)
			}
			if strings.HasSuffix(name, "cloudflared") {
				return []byte("cloudflared version 2026.10.1 (built 2026-10-01)\n"), nil
			}
			return nil, nil
		},
	}
	return e, root, &ran
}

func TestInstallConnectorWritesTokenOnlyToARootFile(t *testing.T) {
	e, root, ran := connectorEngine(t, true, false)
	res, err := e.Connector(context.Background(), ConnectorRequest{Action: ConnectorInstall, MachineID: HostMachineID, TunnelID: testTunnel, Token: "eySECRETTOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unit != "cloudflared-ndl-a0a540581111.service" || !strings.Contains(res.Version, "2026.10.1") {
		t.Fatalf("result %+v", res)
	}
	env := filepath.Join(root, connectorEnvPath(testTunnel))
	st, err := os.Stat(env)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v %v", st, err)
	}
	if b, _ := os.ReadFile(env); string(b) != "TUNNEL_TOKEN=eySECRETTOKEN\n" {
		t.Fatalf("token file %q", b)
	}
	unit, _ := os.ReadFile(filepath.Join(root, "etc/systemd/system", res.Unit))
	if !strings.Contains(string(unit), "EnvironmentFile="+connectorEnvPath(testTunnel)) || strings.Contains(string(unit), "eySECRET") {
		t.Fatalf("unit must load the token from the env file only:\n%s", unit)
	}
	if b, _ := os.ReadFile(filepath.Join(root, cloudflareList)); !strings.Contains(string(b), "signed-by="+cloudflareKeyring) {
		t.Fatalf("apt source must be signed: %s", b)
	}
	joined := strings.Join(*ran, "\n")
	for _, want := range []string{"apt-get install -y -q cloudflared", "systemctl daemon-reload", "systemctl enable --now " + res.Unit} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "eySECRET") {
		t.Fatal("the token must never be passed as a command argument")
	}
}

func TestInstallConnectorUsesExistingBinaryAndNeedsAptOtherwise(t *testing.T) {
	e, _, ran := connectorEngine(t, false, true)
	if _, err := e.Connector(context.Background(), ConnectorRequest{Action: ConnectorInstall, TunnelID: testTunnel, Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(*ran, "\n"), "apt-get") {
		t.Fatal("an installed cloudflared must be reused")
	}
	e2, _, _ := connectorEngine(t, false, false)
	if _, err := e2.Connector(context.Background(), ConnectorRequest{Action: ConnectorInstall, TunnelID: testTunnel, Token: "tok"}); err == nil || !strings.Contains(err.Error(), "no apt") {
		t.Fatalf("no apt and no binary: %v", err)
	}
	if _, err := e.Connector(context.Background(), ConnectorRequest{Action: ConnectorInstall, TunnelID: "../../etc", Token: "tok"}); err == nil {
		t.Fatal("tunnel id must be validated")
	}
	if _, err := e.Connector(context.Background(), ConnectorRequest{Action: ConnectorInstall, TunnelID: testTunnel, Token: "a\nExecStart=/bin/sh"}); err == nil {
		t.Fatal("a token with a newline must be refused")
	}
}

func TestRemoveConnectorOnlyTouchesNoDalUnits(t *testing.T) {
	e, root, ran := connectorEngine(t, false, true)
	if _, err := e.Connector(context.Background(), ConnectorRequest{Action: ConnectorInstall, TunnelID: testTunnel, Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	*ran = nil
	res, err := e.Connector(context.Background(), ConnectorRequest{Action: ConnectorRemove, TunnelID: testTunnel})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, connectorEnvPath(testTunnel))); !os.IsNotExist(err) {
		t.Fatal("the token file must be removed")
	}
	if !strings.Contains(strings.Join(*ran, "\n"), "disable --now "+res.Unit) {
		t.Fatalf("unit must be stopped: %v", *ran)
	}
	if ManagedConnectorUnit("cloudflared.service") || !ManagedConnectorUnit(ConnectorUnit(testTunnel)) {
		t.Fatal("only No-dal units are managed")
	}
}

func TestProbeOnlyDialsIPAddresses(t *testing.T) {
	var dialed string
	e := &Engine{ProbeDial: func(_ context.Context, _ string, addr string) error {
		dialed = addr
		if strings.HasSuffix(addr, ":9") {
			return fmt.Errorf("dial tcp %s: connect: connection refused", addr)
		}
		return nil
	}}
	res, err := e.Connector(context.Background(), ConnectorRequest{Action: ConnectorProbe, MachineID: "ct-1", Host: "localhost", Port: 3000})
	if err != nil || !res.Reachable || dialed != "127.0.0.1:3000" {
		t.Fatalf("%+v %v %s", res, err, dialed)
	}
	res, _ = e.Connector(context.Background(), ConnectorRequest{Action: ConnectorProbe, Host: "10.0.0.5", Port: 9})
	if res.Reachable || !strings.Contains(res.Message, "nothing is listening") {
		t.Fatalf("refused: %+v", res)
	}
	for _, bad := range []ConnectorRequest{{Host: "example.com", Port: 80}, {Host: "169.254.169.254", Port: 80}, {Host: "10.0.0.1", Port: 0}} {
		bad.Action = ConnectorProbe
		if _, err := e.Connector(context.Background(), bad); err == nil {
			t.Fatalf("%+v must be refused", bad)
		}
	}
}

func TestParseTunnelID(t *testing.T) {
	line := `2026-10-12T16:30:37Z INF Starting tunnel tunnelID=A0A54058-1111-4222-8333-444455556666`
	if got := ParseTunnelID(line); got != testTunnel {
		t.Fatal(got)
	}
	if ParseTunnelID("nothing here") != "" {
		t.Fatal("no id")
	}
}
