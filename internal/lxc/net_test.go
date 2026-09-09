package lxc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeIPConfigDefaults(t *testing.T) {
	got, err := NormalizeIPConfig(IPConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got.IPv4Mode != IPModeDHCP || got.IPv6Mode != IPModeDisabled {
		t.Fatalf("%+v", got)
	}
	if RequireStaticGateways(got) != nil {
		t.Fatal("dhcp default must not require gateways")
	}
}

func TestNormalizeIPConfigStatic(t *testing.T) {
	got, err := NormalizeIPConfig(IPConfig{
		IPv4Mode: IPModeStatic, IPv4Address: "192.168.10.20/24", IPv4Gateway: "192.168.10.1",
		IPv6Mode: IPModeDisabled, DNS: []string{"1.1.1.1", "8.8.8.8"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.IPv4Address != "192.168.10.20/24" || got.IPv4Gateway != "192.168.10.1" {
		t.Fatalf("%+v", got)
	}
	if err := RequireStaticGateways(got); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeIPConfig(IPConfig{IPv4Mode: IPModeStatic, IPv4Address: "10.0.0.2"}); err == nil {
		t.Fatal("static without CIDR must fail")
	}
	if err := RequireStaticGateways(IPConfig{IPv4Mode: IPModeStatic, IPv4Address: "10.0.0.2/24"}); err == nil {
		t.Fatal("operator static must require gateway")
	}
}

func TestIPConfigSummary(t *testing.T) {
	s := IPConfig{IPv4Mode: IPModeDHCP}.Summary()
	if !strings.Contains(s, "IPv4 DHCP") || !strings.Contains(s, "IPv6 Disabled") {
		t.Fatal(s)
	}
}

func TestEnsureGuestNetworkStaticAndDNS(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc", "systemd", "network"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := IPConfig{
		IPv4Mode: IPModeStatic, IPv4Address: "10.1.2.8/24", IPv4Gateway: "10.1.2.1",
		IPv6Mode: IPModeDisabled, DNS: []string{"1.1.1.1"},
	}
	if err := ensureGuestNetwork(root, cfg); err != nil {
		t.Fatal(err)
	}
	ifaces, err := os.ReadFile(filepath.Join(root, "etc", "network", "interfaces"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(ifaces)
	if !strings.Contains(body, "iface eth0 inet static") || !strings.Contains(body, "address 10.1.2.8/24") {
		t.Fatal(body)
	}
	if strings.Contains(body, "inet6") {
		t.Fatal(body)
	}
	netd, err := os.ReadFile(filepath.Join(root, "etc", "systemd", "network", "10-eth0.network"))
	if err != nil {
		t.Fatal(err)
	}
	nd := string(netd)
	if !strings.Contains(nd, "Address=10.1.2.8/24") || !strings.Contains(nd, "IPv6AcceptRA=no") {
		t.Fatal(nd)
	}
	drop, err := os.ReadFile(filepath.Join(root, "etc", "systemd", "system", "systemd-networkd.service.d", "zzzz-ndl-lxc.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(drop), "RestrictNamespaces=no") {
		t.Fatal(string(drop))
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "systemd", "system", "ndl-dhclient.service")); err == nil {
		t.Fatal("static IPv4 must not enable the DHCP unit")
	}
	resolv, err := os.ReadFile(filepath.Join(root, "etc", "resolv.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resolv), "nameserver 1.1.1.1") {
		t.Fatal(string(resolv))
	}
}

func TestEnsureGuestNetworkDHCPWritesClientUnit(t *testing.T) {
	root := t.TempDir()
	if err := ensureGuestNetwork(root, IPConfig{}); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(root, "usr", "local", "sbin", "ndl-guest-ipv4"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(script)
	if !strings.Contains(body, "dhclient") || !strings.Contains(body, "udhcpc") {
		t.Fatal(body)
	}
	unit, err := os.ReadFile(filepath.Join(root, "etc", "systemd", "system", "ndl-dhclient.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "ndl-guest-ipv4") {
		t.Fatal(string(unit))
	}
	if _, err := os.Lstat(filepath.Join(root, "etc", "systemd", "system", "multi-user.target.wants", "ndl-dhclient.service")); err != nil {
		t.Fatal(err)
	}
}
