package ndnet

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultNetworkDir = "/etc/systemd/network"
	defaultStateDir   = "/var/lib/ndl/net"
	rollbackUnit      = "ndl-network-rollback.service"
)

// Runner executes a validated argv vector. Tests replace it.
type Runner func(ctx context.Context, name string, args ...string) error

// OutputRunner captures stdout from a typed argv. Tests replace it.
type OutputRunner func(ctx context.Context, name string, args ...string) (string, error)

// Engine applies typed network plans. It never takes a shell string.
type Engine struct {
	Root         string
	NetworkDir   string
	StateDir     string
	SecretDir    string
	Host         func() (HostView, error)
	Run          Runner
	Output       OutputRunner
	Handshake    func(iface string) (int64, error)
	Probe        func() error
	UnitActive   func(name string) bool
	Render       func(Plan) []File
	Now          func() time.Time
	SkipHostCmds bool
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

func (e *Engine) host() (HostView, error) {
	if e.Host != nil {
		return e.Host()
	}
	return CollectHostView(e.Root)
}

func (e *Engine) networkDir() string {
	if e.NetworkDir != "" {
		return e.NetworkDir
	}
	if e.Root != "" && e.Root != "/" {
		return filepath.Join(e.Root, "etc/systemd/network")
	}
	return defaultNetworkDir
}

func (e *Engine) secretDir() string {
	if e.SecretDir != "" {
		return e.SecretDir
	}
	if e.Root != "" && e.Root != "/" {
		return filepath.Join(e.Root, "var/lib/ndl/secrets/wireguard")
	}
	return defaultWGSecretDir
}

// SecretDirOrDefault is the WireGuard private-key directory. It is not a unit path.
func (e *Engine) SecretDirOrDefault() string {
	return e.secretDir()
}

func (e *Engine) stateDir() string {
	if e.StateDir != "" {
		return e.StateDir
	}
	if e.Root != "" && e.Root != "/" {
		return filepath.Join(e.Root, "var/lib/ndl/net")
	}
	return defaultStateDir
}

func (e *Engine) run(ctx context.Context, name string, args ...string) error {
	if e.Run != nil {
		return e.Run(ctx, name, args...)
	}
	if e.SkipHostCmds {
		return nil
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) == 0 {
			return err
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DryRun builds and validates a plan without writing host state.
func (e *Engine) DryRun(_ context.Context, spec Spec) (Preview, error) {
	host, err := e.host()
	if err != nil {
		return Preview{}, err
	}
	plan, err := BuildPlan(spec, host)
	if err != nil {
		return Preview{}, err
	}
	if plan.NAT {
		if err := e.checkNFT(plan); err != nil {
			return Preview{}, err
		}
	}
	prev := PreviewOf(plan)
	if plan.Kind == KindLANBridge {
		prev.HostManagers = e.InspectHostManagers(plan.UplinkIfName)
		prev.StaleUplinks = e.listStaleUplinkClaims(plan.UplinkIfName, plan.NetworkID)
		prev.AlreadyApplied = e.lanAlreadyApplied(plan, host) && len(hostManagerActions(prev.HostManagers)) == 0
		for _, item := range prev.HostManagers {
			prev.Warnings = append(prev.Warnings, item.Manager+": "+item.Detail)
		}
		for _, stale := range prev.StaleUplinks {
			prev.Warnings = append(prev.Warnings, "stale No-dal uplink file "+stale.Path+" also matches "+plan.UplinkIfName)
		}
	}
	return prev, nil
}

// Apply writes persistence files, reloads networkd, and starts isolated services.
func (e *Engine) Apply(ctx context.Context, spec Spec) (ApplyResult, error) {
	host, err := e.host()
	if err != nil {
		return ApplyResult{}, err
	}
	before := host.ManagementIfIndex
	plan, err := BuildPlan(spec, host)
	if err != nil {
		return ApplyResult{}, err
	}
	if plan.Class.RequiresConfirm {
		if !ValidIfName(spec.ConfirmIfName) || !sameIface(spec.ConfirmIfName, plan.UplinkIfName) {
			return ApplyResult{}, fmt.Errorf("typed interface confirmation is required: type %s", plan.UplinkIfName)
		}
	}
	if Isolated(plan.Kind) && before > 0 && plan.ManagementIfIndex != before {
		return ApplyResult{}, fmt.Errorf("management ifindex changed during plan")
	}
	if plan.NAT {
		if err := e.checkNFT(plan); err != nil {
			return ApplyResult{}, err
		}
	}

	managers := []HostManagerConflict{}
	if plan.Kind == KindLANBridge {
		managers = e.InspectHostManagers(plan.UplinkIfName)
		if conflicts := hostManagerConflicts(managers); len(conflicts) > 0 {
			return ApplyResult{}, fmt.Errorf("uplink %s is still owned by %s (%s); move that unit before No-dal can enslave it", plan.UplinkIfName, conflicts[0].Manager, conflicts[0].Path)
		}
		if e.lanAlreadyApplied(plan, host) && len(hostManagerActions(managers)) == 0 {
			_ = e.ensureLANBridgeMAC(ctx, plan, host)
			return ApplyResult{
				NetworkID: plan.NetworkID, Name: plan.Name, Kind: plan.Kind,
				BridgeName: plan.BridgeName, UplinkIfName: plan.UplinkIfName,
				Status: StatusAvailable, Reason: "already applied", AlreadyApplied: true,
				DHCP: plan.DHCP, DNS: plan.DNS, NAT: plan.NAT,
				ManagementIfIndex: host.ManagementIfIndex, ManagementIfName: host.ManagementIfName,
				Warnings: plan.Warnings,
			}, nil
		}
	}

	armed := false
	if plan.Kind == KindLANBridge {
		e.backupResolvIfPresent()
		if _, err := e.armRollback(plan, host); err != nil {
			return ApplyResult{}, err
		}
		armed = true
		if err := e.startWatchdog(ctx); err != nil {
			e.clearRollback()
			return ApplyResult{}, err
		}
	}

	fail := func(err error) (ApplyResult, error) {
		if armed {
			_ = e.RestoreActive()
		} else {
			_ = e.revertOwned(ctx, plan)
		}
		return ApplyResult{}, err
	}

	if plan.Kind == KindLANBridge {
		if _, err := e.migrateUplinkManagers(ctx, plan.UplinkIfName, hostManagerActions(managers)); err != nil {
			return fail(err)
		}
		e.releaseStaleUplinkClaims(plan.UplinkIfName, plan.NetworkID)
		if e.persistMatches(plan) && lanBridgeLive(plan, host) {
			_ = e.ensureLANBridgeMAC(ctx, plan, host)
			return ApplyResult{
				NetworkID: plan.NetworkID, Name: plan.Name, Kind: plan.Kind,
				BridgeName: plan.BridgeName, UplinkIfName: plan.UplinkIfName,
				Status: StatusAvailable, Reason: "already applied", AlreadyApplied: true,
				DHCP: plan.DHCP, DNS: plan.DNS, NAT: plan.NAT,
				ManagementIfIndex: host.ManagementIfIndex, ManagementIfName: host.ManagementIfName,
				RollbackArmed: armed, Warnings: plan.Warnings,
			}, nil
		}
	}
	if err := e.writeFiles(plan); err != nil {
		return fail(err)
	}
	if err := e.reloadNetworkd(); err != nil {
		return fail(err)
	}
	if plan.Kind == KindLANBridge {
		_ = e.ensureLANBridgeMAC(ctx, plan, host)
	}
	if Isolated(plan.Kind) {
		if err := e.ensureIsolatedReady(ctx, plan); err != nil {
			return fail(err)
		}
		if err := e.writeDnsmasq(plan); err != nil {
			return fail(err)
		}
		if err := e.startDnsmasq(ctx, plan.NetworkID); err != nil {
			return fail(err)
		}
		if !e.SkipHostCmds && !e.dnsmasqRunning(plan.NetworkID) {
			time.Sleep(1500 * time.Millisecond)
			if !e.dnsmasqRunning(plan.NetworkID) {
				return fail(fmt.Errorf("isolated DHCP did not start on %s", plan.BridgeName))
			}
		}
	} else {
		_ = e.stopDnsmasq(ctx, plan.NetworkID)
	}
	if plan.NAT {
		if err := e.enableForwarding(); err != nil {
			return fail(err)
		}
		if err := e.applyNFT(plan); err != nil {
			return fail(err)
		}
		if err := e.startNAT(ctx, plan.NetworkID); err != nil {
			return fail(err)
		}
	}

	after, _ := e.host()
	if Isolated(plan.Kind) && before > 0 && after.ManagementIfIndex > 0 && after.ManagementIfIndex != before {
		return fail(fmt.Errorf("management ifindex changed after isolated apply"))
	}
	if armed {
		if err := e.probeManagement(after, plan); err != nil {
			_ = e.RestoreActive()
			return ApplyResult{
				NetworkID: plan.NetworkID, Name: plan.Name, Kind: plan.Kind,
				BridgeName: plan.BridgeName, Status: StatusUnavailable,
				Reason: "probe failed; rolled back", RolledBack: true, RollbackArmed: true,
				ManagementIfIndex: before, ManagementIfName: plan.ManagementIfName,
			}, err
		}
		// Leave the independent watchdog running for ProbeWindow.
		// Do not write active.ok here. Addresses may still be moving
		// onto the LAN-bridge, and the control plane must not cancel rollback.
	}

	return ApplyResult{
		NetworkID:         plan.NetworkID,
		Name:              plan.Name,
		Kind:              plan.Kind,
		BridgeName:        plan.BridgeName,
		UplinkIfName:      firstNonEmpty(plan.UplinkIfName, plan.EgressIfName),
		IPv4CIDR:          plan.IPv4CIDR,
		Gateway:           plan.Gateway,
		Status:            StatusAvailable,
		DHCP:              plan.DHCP,
		DNS:               plan.DNS,
		NAT:               plan.NAT,
		ManagementIfIndex: after.ManagementIfIndex,
		ManagementIfName:  after.ManagementIfName,
		RollbackArmed:     armed,
		Warnings:          plan.Warnings,
		EgressIfName:      plan.EgressIfName,
	}, nil
}

// Observe reports host state for desired networks. Missing is unavailable.
func (e *Engine) Observe(_ context.Context, hints []Hint) (Observation, error) {
	host, err := e.host()
	if err != nil {
		return Observation{}, err
	}
	obs := Observation{ManagementIfIndex: host.ManagementIfIndex, ManagementIfName: managementName(host)}
	for _, hint := range hints {
		item := ObservedNetwork{
			NetworkID:         hint.NetworkID,
			Kind:              hint.Kind,
			BridgeName:        hint.BridgeName,
			Status:            StatusAvailable,
			ManagementIfIndex: host.ManagementIfIndex,
		}
		if hint.BridgeName == "" {
			if name, err := BridgeName(hint.NetworkID); err == nil {
				item.BridgeName = name
			}
		}
		if _, ok := lookup(host, item.BridgeName); !ok {
			if !e.filesPresent(hint.NetworkID) {
				item.Status = StatusUnavailable
				item.Reason = "bridge and persistence files are missing"
			} else {
				item.Status = StatusChecking
				item.Reason = "persistence files exist; bridge is not yet visible"
			}
		}
		if Isolated(hint.Kind) {
			if iface, ok := lookup(host, item.BridgeName); ok {
				if (!iface.Up || !hasIPv4(iface)) && e.filesPresent(hint.NetworkID) && !e.SkipHostCmds {
					_ = e.healIsolated(hint)
					if refreshed, rerr := e.host(); rerr == nil {
						host = refreshed
						iface, ok = lookup(host, item.BridgeName)
					}
				}
				if ok && (!iface.Up || !hasIPv4(iface)) {
					item.Status = StatusWarning
					item.Warnings = append(item.Warnings, "isolated bridge is present but not configured")
				}
			}
			item.DHCPRunning = e.dnsmasqRunning(hint.NetworkID)
			if item.Status == StatusAvailable && !item.DHCPRunning {
				item.Status = StatusWarning
				item.Warnings = append(item.Warnings, "isolated DHCP is not running")
			}
			if hint.Kind == KindIsolatedNAT {
				if e.readForwarding() != "1" {
					item.Status = StatusWarning
					item.Warnings = append(item.Warnings, "IPv4 forwarding is not enabled")
				}
				if !e.nftPresent(hint.NetworkID) {
					item.Status = StatusWarning
					item.Warnings = append(item.Warnings, "isolated NAT rules are not persisted")
				}
			}
		} else if e.dnsmasqPresent(hint.NetworkID) || e.dnsmasqRunning(hint.NetworkID) {
			item.Status = StatusWarning
			item.Warnings = append(item.Warnings, "LAN-bridge must not run isolated DHCP")
		}
		if hint.Kind == KindLANBridge && hint.UplinkIfName != "" {
			if up, ok := lookup(host, hint.UplinkIfName); ok && item.BridgeName != "" && !sameIface(up.Master, item.BridgeName) {
				item.Status = StatusWarning
				item.Warnings = append(item.Warnings, "uplink is not enslaved to "+item.BridgeName)
			}
			if healed, herr := e.healLANBridge(hint); herr == nil && healed {
				item.Warnings = append(item.Warnings, "repaired LAN-bridge MAC and host address persistence")
				if refreshed, rerr := e.host(); rerr == nil {
					host = refreshed
				}
			} else if herr != nil {
				item.Status = StatusWarning
				item.Warnings = append(item.Warnings, "LAN-bridge heal failed: "+herr.Error())
			}
			if up, ok := lookup(host, hint.UplinkIfName); ok {
				if br, ok := lookup(host, item.BridgeName); ok && lanBridgeMAC(host, hint.UplinkIfName) != "" && !sameMAC(br.HardwareAddr, up.HardwareAddr) {
					item.Status = StatusWarning
					item.Warnings = append(item.Warnings, "bridge MAC does not match uplink "+hint.UplinkIfName)
				}
			}
			for _, stale := range e.listStaleUplinkClaims(hint.UplinkIfName, hint.NetworkID) {
				item.Status = StatusWarning
				item.Warnings = append(item.Warnings, "stale No-dal uplink file "+stale.Path+" also matches "+hint.UplinkIfName)
			}
		}
		obs.Networks = append(obs.Networks, item)
	}
	keep := map[string]bool{}
	for _, hint := range hints {
		if id := strings.ToLower(strings.TrimSpace(hint.NetworkID)); id != "" {
			keep[id] = true
		}
	}
	if len(keep) > 0 {
		if orphans := e.sweepOrphanUplinkFiles(keep); len(orphans) > 0 && len(obs.Networks) > 0 {
			for _, orphan := range orphans {
				obs.Networks[0].Warnings = append(obs.Networks[0].Warnings, "removed stale No-dal uplink files for "+orphan.NetworkID)
			}
		}
	}
	return obs, nil
}

// RestoreActive rolls host persistence back from the watchdog snapshot.
func (e *Engine) RestoreActive() error {
	active, err := LoadActiveRollback(e.activePath())
	if err != nil {
		return err
	}
	if err := e.restoreSnapshot(active); err != nil {
		return err
	}
	e.clearRollback()
	return nil
}

func (e *Engine) persistFiles(plan Plan) []File {
	if e.Render != nil {
		return e.Render(plan)
	}
	return plan.Files
}

func (e *Engine) writeFiles(plan Plan) error {
	if err := os.MkdirAll(e.networkDir(), 0755); err != nil {
		return err
	}
	for _, file := range e.persistFiles(plan) {
		if !ownedPersistName(filepath.Base(file.RelPath)) {
			return fmt.Errorf("refusing to write unmanaged networkd file")
		}
		path := filepath.Join(e.networkDir(), filepath.Base(file.RelPath))
		if err := os.WriteFile(path, []byte(file.Body), 0644); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) writeDnsmasq(plan Plan) error {
	dir := filepath.Join(e.stateDir(), "dnsmasq")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, plan.NetworkID+".conf"), []byte(plan.Dnsmasq), 0644)
}

func (e *Engine) filesPresent(id string) bool {
	_, err := os.Stat(filepath.Join(e.networkDir(), persistName(id, ".netdev")))
	return err == nil
}

func (e *Engine) dnsmasqPresent(id string) bool {
	_, err := os.Stat(filepath.Join(e.stateDir(), "dnsmasq", id+".conf"))
	return err == nil
}

func (e *Engine) reloadNetworkd() error {
	_ = e.run(context.Background(), "/usr/bin/systemctl", "enable", "systemd-networkd")
	_ = e.run(context.Background(), "/usr/bin/systemctl", "start", "systemd-networkd")
	return e.run(context.Background(), "/usr/bin/networkctl", "reload")
}

func (e *Engine) healIsolated(hint Hint) error {
	bridge := hint.BridgeName
	if bridge == "" {
		name, err := BridgeName(hint.NetworkID)
		if err != nil {
			return err
		}
		bridge = name
	}
	plan := Plan{
		NetworkID:  hint.NetworkID,
		Kind:       hint.Kind,
		BridgeName: bridge,
		IPv4CIDR:   hint.IPv4CIDR,
		Gateway:    hint.Gateway,
	}
	if err := e.ensureIsolatedReady(context.Background(), plan); err != nil {
		return err
	}
	if hint.NetworkID != "" {
		_ = e.run(context.Background(), "/usr/bin/systemctl", "start", "ndl-dnsmasq@"+hint.NetworkID+".service")
	}
	return nil
}

func (e *Engine) healLANBridge(hint Hint) (bool, error) {
	uplink := strings.TrimSpace(hint.UplinkIfName)
	if !ValidIfName(uplink) {
		return false, nil
	}
	host, err := e.host()
	if err != nil {
		return false, err
	}
	spec := Spec{
		NetworkID:    hint.NetworkID,
		Name:         hint.NetworkID,
		Kind:         KindLANBridge,
		UplinkIfName: uplink,
	}
	plan, err := BuildPlan(spec, host)
	if err != nil {
		return false, err
	}
	changed := false
	if !e.persistMatches(plan) {
		if err := e.writeFiles(plan); err != nil {
			return false, err
		}
		changed = true
		if err := e.reloadNetworkd(); err != nil {
			return true, err
		}
	}
	if err := e.ensureLANBridgeMAC(context.Background(), plan, host); err != nil {
		return changed, err
	}
	if live, ok := lookup(host, plan.BridgeName); ok {
		if mac := lanBridgeMAC(host, uplink); mac != "" && !sameMAC(live.HardwareAddr, mac) {
			changed = true
		}
	}
	return changed, nil
}

func (e *Engine) ensureLANBridgeMAC(ctx context.Context, plan Plan, host HostView) error {
	mac := lanBridgeMAC(host, plan.UplinkIfName)
	if mac == "" || !ValidIfName(plan.BridgeName) {
		return nil
	}
	if live, ok := lookup(host, plan.BridgeName); ok && sameMAC(live.HardwareAddr, mac) {
		return nil
	}
	return e.run(ctx, ipBin(), "link", "set", "dev", plan.BridgeName, "address", mac)
}

func (e *Engine) ensureIsolatedReady(ctx context.Context, plan Plan) error {
	if e.SkipHostCmds {
		return nil
	}
	if err := e.waitIsolatedReady(ctx, plan, 4*time.Second); err == nil {
		return nil
	}
	_ = e.run(ctx, "/usr/bin/networkctl", "reconfigure", plan.BridgeName)
	if err := e.waitIsolatedReady(ctx, plan, 4*time.Second); err == nil {
		return nil
	}
	prefix := plan.IPv4CIDR
	if prefix == "" {
		prefix = plan.Gateway + "/24"
	} else if plan.Gateway != "" {
		if _, bits, ok := strings.Cut(prefix, "/"); ok {
			prefix = plan.Gateway + "/" + bits
		}
	}
	ip := ipBin()
	_ = e.run(ctx, ip, "link", "set", "dev", plan.BridgeName, "up")
	if prefix != "" {
		_ = e.run(ctx, ip, "addr", "replace", prefix, "dev", plan.BridgeName)
	}
	return e.waitIsolatedReady(ctx, plan, 6*time.Second)
}

func (e *Engine) waitIsolatedReady(ctx context.Context, plan Plan, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		if isolatedBridgeReady(plan.BridgeName, plan.Gateway) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("isolated bridge %s did not become ready", plan.BridgeName)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func isolatedBridgeReady(name, gateway string) bool {
	if name == "" {
		return false
	}
	iface, err := net.InterfaceByName(name)
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return false
	}
	if gateway == "" {
		return true
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if strings.HasPrefix(a.String(), gateway+"/") || a.String() == gateway {
			return true
		}
	}
	return false
}

func ipBin() string {
	for _, path := range []string{"/usr/sbin/ip", "/usr/bin/ip", "/sbin/ip"} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "/usr/sbin/ip"
}

func hasIPv4(iface Iface) bool {
	for _, addr := range iface.Addresses {
		ip, _, err := net.ParseCIDR(addr)
		if err != nil {
			ip = net.ParseIP(addr)
		}
		if ip != nil && ip.To4() != nil {
			return true
		}
	}
	return false
}

func (e *Engine) startWatchdog(ctx context.Context) error {
	return e.run(ctx, "/usr/bin/systemctl", "start", "--no-block", rollbackUnit)
}

func (e *Engine) stopWatchdog(ctx context.Context) error {
	return e.run(ctx, "/usr/bin/systemctl", "stop", rollbackUnit)
}

func (e *Engine) startDnsmasq(ctx context.Context, id string) error {
	if _, err := uuidParse(id); err != nil {
		return err
	}
	unit := "ndl-dnsmasq@" + id + ".service"
	_ = e.run(ctx, "/usr/bin/systemctl", "reset-failed", unit)
	_ = e.run(ctx, "/usr/bin/systemctl", "enable", unit)
	return e.run(ctx, "/usr/bin/systemctl", "start", unit)
}

func (e *Engine) stopDnsmasq(ctx context.Context, id string) error {
	if _, err := uuidParse(id); err != nil {
		return nil
	}
	return e.run(ctx, "/usr/bin/systemctl", "stop", "ndl-dnsmasq@"+id+".service")
}

func (e *Engine) checkNFT(plan Plan) error {
	if plan.NFT == "" {
		return nil
	}
	path, err := e.writeNFTNamed(plan.NetworkID+".check.nft", plan.NFT)
	if err != nil {
		return err
	}
	err = e.run(context.Background(), NFTBin, "-c", "-f", path)
	_ = os.Remove(path)
	return err
}

func (e *Engine) applyNFT(plan Plan) error {
	if plan.NFT == "" {
		return fmt.Errorf("isolated-nat nftables rules are empty")
	}
	path, err := e.writeNFTNamed(plan.NetworkID+".nft", plan.NFT)
	if err != nil {
		return err
	}
	if destroy := renderNFTDestroy(plan); destroy != "" {
		if _, err := e.writeNFTNamed(plan.NetworkID+".destroy.nft", destroy); err != nil {
			return err
		}
	}
	if err := e.run(context.Background(), NFTBin, "-c", "-f", path); err != nil {
		return err
	}
	return e.run(context.Background(), NFTBin, "-f", path)
}

func (e *Engine) nftPresent(id string) bool {
	_, err := os.Stat(filepath.Join(e.stateDir(), "nft", id+".nft"))
	return err == nil
}

func (e *Engine) startNAT(ctx context.Context, id string) error {
	if _, err := uuidParse(id); err != nil {
		return err
	}
	unit := "ndl-nat@" + id + ".service"
	_ = e.run(ctx, "/usr/bin/systemctl", "reset-failed", unit)
	_ = e.run(ctx, "/usr/bin/systemctl", "enable", unit)
	return e.run(ctx, "/usr/bin/systemctl", "start", unit)
}

func (e *Engine) stopNAT(ctx context.Context, id string) error {
	if _, err := uuidParse(id); err != nil {
		return nil
	}
	unit := "ndl-nat@" + id + ".service"
	_ = e.run(ctx, "/usr/bin/systemctl", "disable", "--now", unit)
	return e.run(ctx, "/usr/bin/systemctl", "stop", unit)
}

// RestoreNAT reapplies persisted isolated-nat tables and IPv4 forwarding.
func (e *Engine) RestoreNAT(ctx context.Context) error {
	dir := filepath.Join(e.stateDir(), "nft")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	any := false
	for _, ent := range entries {
		if ent.IsDir() || !natPersistName(ent.Name()) {
			continue
		}
		any = true
		_ = e.run(ctx, NFTBin, "-f", filepath.Join(dir, ent.Name()))
	}
	if !any {
		return nil
	}
	return e.enableForwarding()
}

func (e *Engine) probeManagement(host HostView, plan Plan) error {
	if e.Probe != nil {
		return e.Probe()
	}
	return ProbeManagement(host, plan.ManagementIfName, plan.ManagementIfIndex, host.ManagementAddresses...)
}

// RecoverStale restores a timed-out failed apply, or restarts the watchdog
// when the 120s probe window is still open.
func (e *Engine) RecoverStale(now time.Time) error {
	active, err := LoadActiveRollback(e.activePath())
	if err != nil {
		return nil
	}
	if _, err := os.Stat(e.okPath()); err == nil {
		return nil
	}
	if now.IsZero() {
		now = e.now()
	}
	if now.Before(active.Deadline) {
		return e.startWatchdog(context.Background())
	}
	host, herr := e.host()
	if herr == nil && ProbeManagement(host, active.ManagementIfName, active.ManagementIfIndex, active.ManagementAddresses...) == nil {
		e.clearRollback()
		return nil
	}
	return e.RestoreActive()
}

func (e *Engine) revertOwned(ctx context.Context, plan Plan) error {
	for _, file := range e.persistFiles(plan) {
		_ = os.Remove(filepath.Join(e.networkDir(), filepath.Base(file.RelPath)))
	}
	_ = os.Remove(filepath.Join(e.stateDir(), "dnsmasq", plan.NetworkID+".conf"))
	_ = e.stopDnsmasq(ctx, plan.NetworkID)
	if plan.NAT || e.nftPresent(plan.NetworkID) {
		_ = e.removeNAT(ctx, plan)
	}
	return e.reloadNetworkd()
}

// Delete removes No-dal-owned files, NAT rules, and forwarding for one network.
func (e *Engine) Delete(ctx context.Context, spec Spec) error {
	id := strings.TrimSpace(spec.NetworkID)
	plan := Plan{NetworkID: id, Kind: spec.Kind, NAT: spec.Kind == KindIsolatedNAT}
	if br, err := BridgeName(id); err == nil {
		plan.BridgeName = br
	}
	switch spec.Kind {
	case KindLANBridge:
		plan.Files = lanBridgeFiles(id, plan.BridgeName, spec.UplinkIfName, HostView{})
	default:
		plan.Files = []File{
			{RelPath: persistName(id, ".netdev")},
			{RelPath: persistName(id, ".network")},
		}
	}
	return e.revertOwned(ctx, plan)
}

func (e *Engine) removeNAT(ctx context.Context, plan Plan) error {
	_ = e.stopNAT(ctx, plan.NetworkID)
	destroy := renderNFTDestroy(plan)
	if destroy != "" {
		path, err := e.writeNFTNamed(plan.NetworkID+".destroy.nft", destroy)
		if err == nil {
			_ = e.run(ctx, NFTBin, "-f", path)
		}
	}
	_ = os.Remove(filepath.Join(e.stateDir(), "nft", plan.NetworkID+".nft"))
	_ = os.Remove(filepath.Join(e.stateDir(), "nft", plan.NetworkID+".destroy.nft"))
	_ = os.Remove(filepath.Join(e.stateDir(), "nft", plan.NetworkID+".check.nft"))
	return e.disableForwardingIfUnused()
}

func (e *Engine) dnsmasqRunning(id string) bool {
	unit := "ndl-dnsmasq@" + id + ".service"
	if e.UnitActive != nil {
		return e.UnitActive(unit)
	}
	if e.SkipHostCmds {
		return false
	}
	return e.run(context.Background(), "/usr/bin/systemctl", "is-active", "--quiet", unit) == nil
}

func (e *Engine) writeNFTNamed(name, rules string) (string, error) {
	dir := filepath.Join(e.stateDir(), "nft")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, []byte(rules), 0600)
}

func uuidParse(id string) (string, error) {
	if _, err := BridgeName(id); err != nil {
		return "", err
	}
	return id, nil
}
