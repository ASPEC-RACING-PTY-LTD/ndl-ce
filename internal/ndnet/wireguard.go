package ndnet

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	ActionWGApply  = "wg-apply"
	ActionWGStatus = "wg-status"
	ActionWGRemove = "wg-remove"

	WGBin              = "/usr/bin/wg"
	defaultWGSecretDir = "/var/lib/ndl/secrets/wireguard"
	DefaultWGPort      = 51820
	DefaultWGKeepalive = 25
	DefaultWGCIDR      = "10.64.8.0/24"
	HandshakeFreshSecs = 180
	SessionFreshSecs   = 90

	NodeReady    = "Ready"
	NodeNotReady = "NotReady"

	WGSkipReason = "wireguard handshake not observed"
	WGJoinLater  = "WireGuard is pre-join connectivity. Cluster join remains Phase 30."
)

// WGPeerSpec is one remote peer. PublicKey is identity material, not a secret.
type WGPeerSpec struct {
	PublicKey           string `json:"public_key"`
	Endpoint            string `json:"endpoint,omitempty"`
	AllowedIPs          string `json:"allowed_ips,omitempty"`
	PersistentKeepalive uint32 `json:"persistent_keepalive,omitempty"`
}

// WGOp is a typed WireGuard action. There is no private-key field.
type WGOp struct {
	Action         string       `json:"action"`
	PeerID         string       `json:"peer_id"`
	ListenPort     uint32       `json:"listen_port,omitempty"`
	AddressCIDR    string       `json:"address_cidr,omitempty"`
	PrivateKeyFile string       `json:"private_key_file,omitempty"`
	Peers          []WGPeerSpec `json:"peers,omitempty"`
}

// WGResult is the honest apply or status outcome. Private key material is absent.
type WGResult struct {
	Action            string   `json:"action"`
	PeerID            string   `json:"peer_id"`
	Status            string   `json:"status"`
	Reason            string   `json:"reason,omitempty"`
	Locator           string   `json:"locator,omitempty"`
	PublicKey         string   `json:"public_key,omitempty"`
	ListenPort        uint32   `json:"listen_port,omitempty"`
	AddressCIDR       string   `json:"address_cidr,omitempty"`
	PrivateKeyPath    string   `json:"private_key_path,omitempty"`
	LastHandshakeUnix int64    `json:"last_handshake_unix"`
	Files             []File   `json:"files,omitempty"`
	Argv              []string `json:"argv,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

// WGName is the Linux locator for a WireGuard UUID. UUID remains identity.
func WGName(id string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return "", fmt.Errorf("wireguard peer id must be a UUID")
	}
	hex := strings.ReplaceAll(parsed.String(), "-", "")
	name := "ndlw" + hex[:8]
	if !ValidIfName(name) {
		return "", fmt.Errorf("wireguard interface name is invalid")
	}
	return name, nil
}

// WGPrivateKeyPath is a 0600 secret file. It is not a systemd unit.
func WGPrivateKeyPath(dir, id string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return "", fmt.Errorf("wireguard peer id must be a UUID")
	}
	if dir == "" {
		dir = defaultWGSecretDir
	}
	return filepath.Join(dir, strings.ToLower(parsed.String())+".key"), nil
}

// ApplyWireGuard executes one typed WireGuard action.
func (e *Engine) ApplyWireGuard(ctx context.Context, op WGOp) (WGResult, error) {
	switch strings.ToLower(strings.TrimSpace(op.Action)) {
	case ActionWGApply:
		return e.applyWG(ctx, op)
	case ActionWGStatus:
		return e.statusWG(ctx, op)
	case ActionWGRemove:
		return e.removeWG(ctx, op)
	default:
		return WGResult{}, fmt.Errorf("wireguard action is unsupported")
	}
}

func (e *Engine) applyWG(ctx context.Context, op WGOp) (WGResult, error) {
	id := strings.ToLower(strings.TrimSpace(op.PeerID))
	ifname, err := WGName(id)
	if err != nil {
		return WGResult{}, err
	}
	port := op.ListenPort
	if port == 0 {
		port = DefaultWGPort
	}
	if port < 1 || port > 65535 {
		return WGResult{}, fmt.Errorf("wireguard listen port is invalid")
	}
	addr := strings.TrimSpace(op.AddressCIDR)
	if addr == "" {
		addr = "10.64.8.1/24"
	}
	if err := ParseWGAddress(addr); err != nil {
		return WGResult{}, err
	}
	for i, p := range op.Peers {
		if err := validateWGPeer(p); err != nil {
			return WGResult{}, fmt.Errorf("peer %d: %w", i, err)
		}
	}
	keyPath := strings.TrimSpace(op.PrivateKeyFile)
	if keyPath == "" {
		keyPath, err = WGPrivateKeyPath(e.secretDir(), id)
		if err != nil {
			return WGResult{}, err
		}
	}
	if err := refuseKeyInUnit(keyPath); err != nil {
		return WGResult{}, err
	}
	pub, err := e.ensureWGPrivateKey(keyPath)
	if err != nil {
		return WGResult{}, err
	}
	files := wgFiles(id, ifname, keyPath, port, addr, op.Peers)
	if err := refusePrivateKeyInFiles(files); err != nil {
		return WGResult{}, err
	}
	if err := e.writeOwned(files); err != nil {
		return WGResult{}, err
	}
	if err := e.reloadNetworkd(); err != nil && !e.SkipHostCmds {
		return WGResult{}, err
	}
	hs, argv := e.readHandshake(ctx, ifname)
	status := StatusUnavailable
	reason := WGSkipReason
	if hs > 0 && handshakeFresh(hs, e.now().Unix()) {
		status = StatusAvailable
		reason = ""
	}
	if e.SkipHostCmds && hs == 0 {
		reason = WGSkipReason
	}
	return WGResult{
		Action: ActionWGApply, PeerID: id, Status: status, Reason: reason, Locator: ifname,
		PublicKey: pub, ListenPort: port, AddressCIDR: addr, PrivateKeyPath: keyPath,
		LastHandshakeUnix: hs, Files: files, Argv: argv, Warnings: []string{WGJoinLater},
	}, nil
}

func (e *Engine) statusWG(ctx context.Context, op WGOp) (WGResult, error) {
	id := strings.ToLower(strings.TrimSpace(op.PeerID))
	ifname, err := WGName(id)
	if err != nil {
		return WGResult{}, err
	}
	keyPath, err := WGPrivateKeyPath(e.secretDir(), id)
	if err != nil {
		return WGResult{}, err
	}
	pub := ""
	if b, err := os.ReadFile(keyPath); err == nil {
		if derived, err := PublicFromPrivate(strings.TrimSpace(string(b))); err == nil {
			pub = derived
		}
	}
	hs, argv := e.readHandshake(ctx, ifname)
	status := StatusUnavailable
	reason := WGSkipReason
	if hs > 0 && handshakeFresh(hs, e.now().Unix()) {
		status = StatusAvailable
		reason = ""
	}
	return WGResult{
		Action: ActionWGStatus, PeerID: id, Status: status, Reason: reason, Locator: ifname,
		PublicKey: pub, PrivateKeyPath: keyPath, LastHandshakeUnix: hs, Argv: argv,
	}, nil
}

func (e *Engine) removeWG(_ context.Context, op WGOp) (WGResult, error) {
	id := strings.ToLower(strings.TrimSpace(op.PeerID))
	ifname, err := WGName(id)
	if err != nil {
		return WGResult{}, err
	}
	for _, suffix := range []string{"-wg.netdev", "-wg.network"} {
		_ = os.Remove(filepath.Join(e.networkDir(), persistName(id, suffix)))
	}
	if err := e.reloadNetworkd(); err != nil && !e.SkipHostCmds {
		return WGResult{}, err
	}
	return WGResult{Action: ActionWGRemove, PeerID: id, Locator: ifname, Status: StatusUnavailable, Reason: "removed"}, nil
}

func (e *Engine) ensureWGPrivateKey(path string) (public string, err error) {
	if b, err := os.ReadFile(path); err == nil {
		priv := strings.TrimSpace(string(b))
		if priv != "" {
			return PublicFromPrivate(priv)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	priv, pub, err := GenerateWGKey()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(priv+"\n"), 0600); err != nil {
		return "", err
	}
	return pub, nil
}

func (e *Engine) readHandshake(ctx context.Context, ifname string) (int64, []string) {
	if e.Handshake != nil {
		n, err := e.Handshake(ifname)
		if err != nil {
			return 0, nil
		}
		return n, []string{WGBin, "show", ifname, "latest-handshakes"}
	}
	if e.SkipHostCmds {
		return 0, nil
	}
	argv := []string{WGBin, "show", ifname, "latest-handshakes"}
	if err := refuseShellArgv(argv); err != nil {
		return 0, argv
	}
	out, err := e.runOutput(ctx, argv[0], argv[1:]...)
	if err != nil {
		return 0, argv
	}
	return parseLatestHandshakes(out), argv
}

func (e *Engine) runOutput(ctx context.Context, name string, args ...string) (string, error) {
	if e.Output != nil {
		return e.Output(ctx, name, args...)
	}
	if e.SkipHostCmds {
		return "", nil
	}
	cmd := exec.CommandContext(ctx, name, args...)
	b, err := cmd.Output()
	return string(b), err
}

func wgFiles(id, ifname, keyPath string, port uint32, addr string, peers []WGPeerSpec) []File {
	var b strings.Builder
	b.WriteString("[NetDev]\nName=" + ifname + "\nKind=wireguard\n\n")
	b.WriteString("[WireGuard]\nPrivateKeyFile=" + keyPath + "\nListenPort=" + strconv.FormatUint(uint64(port), 10) + "\n")
	for _, p := range peers {
		b.WriteString("\n[WireGuardPeer]\nPublicKey=" + p.PublicKey + "\n")
		if p.AllowedIPs != "" {
			b.WriteString("AllowedIPs=" + p.AllowedIPs + "\n")
		}
		if p.Endpoint != "" {
			b.WriteString("Endpoint=" + p.Endpoint + "\n")
		}
		ka := p.PersistentKeepalive
		if ka == 0 {
			ka = DefaultWGKeepalive
		}
		b.WriteString("PersistentKeepalive=" + strconv.FormatUint(uint64(ka), 10) + "\n")
	}
	network := "[Match]\nName=" + ifname + "\n\n[Network]\nAddress=" + addr + "\n"
	return []File{
		{RelPath: persistName(id, "-wg.netdev"), Body: b.String()},
		{RelPath: persistName(id, "-wg.network"), Body: network},
	}
}

func validateWGPeer(p WGPeerSpec) error {
	if !ValidWGPublicKey(p.PublicKey) {
		return fmt.Errorf("peer public key is invalid")
	}
	if p.AllowedIPs != "" {
		if err := ParseWGAddress(p.AllowedIPs); err != nil {
			return err
		}
	}
	return ValidWGEndpoint(p.Endpoint)
}

func validHostPort(value, kind string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.Contains(value, "@") || strings.Contains(value, "/") {
		return fmt.Errorf("%s must be host:port without credentials", kind)
	}
	host, port, ok := strings.Cut(value, ":")
	if !ok || host == "" || strings.ContainsAny(host, " \t\n") {
		return fmt.Errorf("%s is invalid", kind)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%s port is invalid", kind)
	}
	return nil
}

// ValidWGEndpoint reports a WireGuard host:port without userinfo.
func ValidWGEndpoint(endpoint string) error {
	return validHostPort(endpoint, "peer endpoint")
}

// ValidListenAddr reports a remote listen host:port without userinfo.
func ValidListenAddr(addr string) error {
	return validHostPort(addr, "listen address")
}

// ParseWGListenPort accepts 1-65535, or 0 which defaults to DefaultWGPort.
func ParseWGListenPort(port int) (int, error) {
	if port == 0 {
		return DefaultWGPort, nil
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("wireguard listen port is invalid")
	}
	return port, nil
}

// ParseWGAddress requires a CIDR locator such as 10.64.8.1/24.
func ParseWGAddress(cidr string) error {
	if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
		return fmt.Errorf("wireguard address must be CIDR")
	}
	return nil
}

func refusePrivateKeyInFiles(files []File) error {
	for _, f := range files {
		for _, line := range strings.Split(f.Body, "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "PrivateKey=") && !strings.HasPrefix(trim, "PrivateKeyFile=") {
				return fmt.Errorf("wireguard private keys must not appear in networkd files")
			}
		}
	}
	return nil
}

func refuseKeyInUnit(path string) error {
	if strings.Contains(path, "/systemd/") || strings.HasSuffix(path, ".netdev") || strings.HasSuffix(path, ".network") || strings.HasSuffix(path, ".service") {
		return fmt.Errorf("wireguard private key file must not be a unit file")
	}
	return nil
}

func parseLatestHandshakes(out string) int64 {
	var latest int64
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
		if err != nil {
			continue
		}
		if n > latest {
			latest = n
		}
	}
	return latest
}

func handshakeFresh(hs, now int64) bool {
	if hs <= 0 || now <= 0 {
		return false
	}
	return now-hs <= HandshakeFreshSecs
}

// SessionFresh reports whether a remote OpenSession is still live.
func SessionFresh(lastSeenUnix, now int64) bool {
	if lastSeenUnix <= 0 || now <= 0 {
		return false
	}
	return now-lastSeenUnix <= SessionFreshSecs
}

// RemoteReady is Ready only when both the tunnel handshake and the session are live.
func RemoteReady(handshakeUnix, lastSeenUnix, now int64) bool {
	return handshakeFresh(handshakeUnix, now) && SessionFresh(lastSeenUnix, now)
}
