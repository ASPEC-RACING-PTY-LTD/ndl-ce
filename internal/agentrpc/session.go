package agentrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/ndnet"
)

// DesiredWGPath is the worker drop-in used before cluster join.
func DesiredWGPath(dataDir string) string {
	return filepath.Join(dataDir, "wireguard", "desired.json")
}

// WGDesired is the worker apply document. PrivateKey is a secret on disk.
type WGDesired struct {
	PeerID       string             `json:"peer_id"`
	ListenPort   uint32             `json:"listen_port"`
	AddressCIDR  string             `json:"address_cidr"`
	PrivateKey   string             `json:"private_key,omitempty"`
	ControlURL   string             `json:"control_url"`
	PairingToken string             `json:"pairing_token"`
	ListenAddr   string             `json:"listen_addr"`
	Peers        []ndnet.WGPeerSpec `json:"peers"`
}

// SessionLoop applies desired WireGuard and dials OpenSession to the control plane.
func (h *Handler) SessionLoop(dataDir string, every time.Duration) {
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	_ = h.ApplyDesiredSession(context.Background(), dataDir)
	for range t.C {
		_ = h.ApplyDesiredSession(context.Background(), dataDir)
	}
}

// ApplyDesiredSession reads the worker drop-in, applies WireGuard, and dials out.
func (h *Handler) ApplyDesiredSession(ctx context.Context, dataDir string) error {
	path := DesiredWGPath(dataDir)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var desired WGDesired
	if err := json.Unmarshal(b, &desired); err != nil {
		return err
	}
	op := ndnet.WGOp{
		Action: ndnet.ActionWGApply, PeerID: desired.PeerID, ListenPort: desired.ListenPort,
		AddressCIDR: desired.AddressCIDR, Peers: desired.Peers,
	}
	if strings.TrimSpace(desired.PrivateKey) != "" {
		keyPath, err := ndnet.WGPrivateKeyPath(h.nets().SecretDirOrDefault(), desired.PeerID)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(keyPath, []byte(strings.TrimSpace(desired.PrivateKey)+"\n"), 0600); err != nil {
			return err
		}
		op.PrivateKeyFile = keyPath
	}
	res, err := h.nets().ApplyWireGuard(ctx, op)
	if err != nil {
		return err
	}
	listen := strings.TrimSpace(desired.ListenAddr)
	if listen == "" && res.AddressCIDR != "" {
		listen = strings.Split(res.AddressCIDR, "/")[0] + ":9444"
	}
	nodeID, clusterID, _ := h.Ident.LoadNode()
	return DialOpenSession(ctx, desired.ControlURL, map[string]any{
		"peer_id": desired.PeerID, "node_id": nodeID, "cluster_id": clusterID,
		"listen_addr": listen, "wg_public_key": res.PublicKey,
		"handshake_unix": res.LastHandshakeUnix, "pairing_token": desired.PairingToken,
	})
}

// DialOpenSession POSTs the agent dial-out session to the control plane.
func DialOpenSession(ctx context.Context, controlURL string, body map[string]any) error {
	controlURL = strings.TrimRight(strings.TrimSpace(controlURL), "/")
	if controlURL == "" {
		controlURL = strings.TrimRight(strings.TrimSpace(os.Getenv("NDL_CONTROL_URL")), "/")
	}
	if controlURL == "" {
		return fmt.Errorf("control url is required for OpenSession dial-out")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controlURL+"/api/v1/cluster/sessions", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("OpenSession dial-out status %d", res.StatusCode)
	}
	return nil
}
