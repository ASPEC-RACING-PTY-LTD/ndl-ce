package ubuntu

import (
	"fmt"
	"strings"
)

const (
	ID             = "ubuntu"
	LTS            = "24.04"
	Family         = "debian"
	PackageTool    = "apt"
	NetworkPersist = "netplan"
)

// QualificationGaps are why Ubuntu LTS is not Tier 1. Do not invent support.
var QualificationGaps = []string{
	"Netplan and systemd-networkd must not be dual-written on the same host",
	"Ubuntu package and repo layout is not proven on the Cloud runner",
	"DKMS and NVIDIA driver install differ from Debian 13",
	"One-line bootstrap remains Debian 13 amd64 until qualification passes",
}

// Is reports whether id is Ubuntu. It is not a support claim.
func Is(id string) bool {
	return strings.EqualFold(strings.TrimSpace(id), ID)
}

// RefuseDebianNetworkd is the Phase 29 recovery gate.
func RefuseDebianNetworkd(hostID, destDir string) error {
	hostID = strings.ToLower(strings.TrimSpace(hostID))
	dest := strings.TrimSpace(destDir)
	if hostID == "debian" || strings.Contains(dest, "/etc/systemd/network") && hostID == "debian" {
		return fmt.Errorf("ubuntu adapter must not rewrite a Debian host's network persistence")
	}
	if strings.Contains(dest, "/etc/systemd/network") {
		return fmt.Errorf("ubuntu adapter must not dual-write systemd-networkd; Netplan adopt is unqualified")
	}
	return fmt.Errorf("ubuntu LTS is not a qualified No-dal host")
}
