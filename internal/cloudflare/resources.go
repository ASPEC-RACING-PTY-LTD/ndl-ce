package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// TokenStatus is the result of verifying a token.
type TokenStatus struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	ExpiresOn *time.Time `json:"expires_on,omitempty"`
	NotBefore *time.Time `json:"not_before,omitempty"`
	// AccountOwned is true when the token verified against an account
	// rather than a user (Cloudflare's account API tokens).
	AccountOwned bool `json:"-"`
}

// Account is a Cloudflare account the token can see.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Zone is a domain in a Cloudflare account.
type Zone struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	Account Account `json:"account"`
}

// Tunnel is a Cloudflare Tunnel (cfd_tunnel).
type Tunnel struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	AccountTag      string     `json:"account_tag"`
	Status          string     `json:"status"`
	ConfigSrc       string     `json:"config_src"`
	RemoteConfig    bool       `json:"remote_config"`
	TunType         string     `json:"tun_type"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	ConnsActiveAt   *time.Time `json:"conns_active_at,omitempty"`
	ConnsInactiveAt *time.Time `json:"conns_inactive_at,omitempty"`
	// Connections is the edge connections the tunnel list reports, which
	// saves a call per tunnel. Not every response includes them.
	Connections []ListedConnection `json:"connections,omitempty"`
}

// ListedConnection is one edge connection in the tunnel list.
type ListedConnection struct {
	ClientID      string     `json:"client_id"`
	ClientVersion string     `json:"client_version"`
	ColoName      string     `json:"colo_name"`
	OriginIP      string     `json:"origin_ip"`
	OpenedAt      *time.Time `json:"opened_at,omitempty"`
	Pending       bool       `json:"is_pending_reconnect"`
}

// ListedConnectors groups the list's connections by connector. ok is false
// when the list had none and the tunnel reports being connected, so the
// connections must be read separately.
func (t Tunnel) ListedConnectors() (out []Connector, ok bool) {
	byID := map[string]int{}
	for _, lc := range t.Connections {
		if lc.Pending {
			continue
		}
		i, seen := byID[lc.ClientID]
		if !seen {
			i = len(out)
			byID[lc.ClientID] = i
			out = append(out, Connector{ID: lc.ClientID, Version: lc.ClientVersion})
		}
		out[i].Connections = append(out[i].Connections, EdgeConnection{ColoName: lc.ColoName, OriginIP: lc.OriginIP, OpenedAt: lc.OpenedAt})
	}
	if len(out) == 0 && (t.Status == "healthy" || t.Status == "degraded") {
		return nil, false
	}
	if out == nil {
		out = []Connector{}
	}
	return out, true
}

// RemotelyManaged reports whether the tunnel's routes live in Cloudflare
// (editable through the API) rather than a config file on the connector.
func (t Tunnel) RemotelyManaged() bool {
	return t.ConfigSrc == "cloudflare" || (t.ConfigSrc == "" && t.RemoteConfig)
}

// Connector is one cloudflared process connected to a tunnel.
type Connector struct {
	ID          string           `json:"id"`
	Version     string           `json:"version"`
	Arch        string           `json:"arch"`
	RunAt       *time.Time       `json:"run_at,omitempty"`
	ConfigVer   int              `json:"config_version"`
	Connections []EdgeConnection `json:"conns"`
}

// EdgeConnection is one connection from a connector to a Cloudflare data centre.
type EdgeConnection struct {
	ColoName string     `json:"colo_name"`
	OriginIP string     `json:"origin_ip"`
	OpenedAt *time.Time `json:"opened_at,omitempty"`
}

// TunnelConfig is a tunnel's remote configuration.
type TunnelConfig struct {
	Version int        `json:"version"`
	Source  string     `json:"source"`
	Config  ConfigBody `json:"config"`
}

// ConfigBody is the config object PUT to /configurations. Unknown fields
// are kept so a round trip never drops settings this package does not model.
type ConfigBody struct {
	Ingress       []IngressRule   `json:"ingress"`
	OriginRequest json.RawMessage `json:"originRequest,omitempty"`
	WarpRouting   json.RawMessage `json:"warp-routing,omitempty"`
}

// DNSRecord is one DNS record in a zone.
type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	ZoneID  string `json:"zone_id,omitempty"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl,omitempty"`
	Comment string `json:"comment,omitempty"`
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func validID(kind, id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("%s id is invalid", kind)
	}
	return nil
}

// VerifyToken checks the token. A user token verifies at /user/tokens/verify;
// an account token only verifies against its account, so accountHint is tried
// when the user endpoint rejects it.
func (c *Client) VerifyToken(ctx context.Context, accountHint string) (TokenStatus, error) {
	var st TokenStatus
	_, err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, nil, &st)
	if err == nil {
		return st, nil
	}
	if !errors.Is(err, ErrAuth) && !errors.Is(err, ErrPermission) || accountHint == "" {
		return st, err
	}
	if verr := validID("account", accountHint); verr != nil {
		return st, verr
	}
	var ast TokenStatus
	if _, aerr := c.do(ctx, http.MethodGet, "/accounts/"+accountHint+"/tokens/verify", nil, nil, &ast); aerr != nil {
		return st, aerr
	}
	ast.AccountOwned = true
	return ast, nil
}

// Accounts lists the accounts the token can see.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	return paged[Account](ctx, c, "/accounts", nil, 50)
}

// Account reads one account (used for account tokens that cannot list).
func (c *Client) Account(ctx context.Context, id string) (Account, error) {
	var a Account
	if err := validID("account", id); err != nil {
		return a, err
	}
	_, err := c.do(ctx, http.MethodGet, "/accounts/"+id, nil, nil, &a)
	return a, err
}

// Zones lists the zones in an account the token can see.
func (c *Client) Zones(ctx context.Context, accountID string) ([]Zone, error) {
	if err := validID("account", accountID); err != nil {
		return nil, err
	}
	return paged[Zone](ctx, c, "/zones", url.Values{"account.id": {accountID}}, 50)
}

// Tunnels lists an account's cloudflared tunnels that are not deleted.
func (c *Client) Tunnels(ctx context.Context, accountID string) ([]Tunnel, error) {
	if err := validID("account", accountID); err != nil {
		return nil, err
	}
	return paged[Tunnel](ctx, c, "/accounts/"+accountID+"/cfd_tunnel", url.Values{"is_deleted": {"false"}}, 100)
}

// CreateTunnel creates a remotely managed tunnel.
func (c *Client) CreateTunnel(ctx context.Context, accountID, name string) (Tunnel, error) {
	var t Tunnel
	if err := validID("account", accountID); err != nil {
		return t, err
	}
	if err := ValidTunnelName(name); err != nil {
		return t, err
	}
	_, err := c.do(ctx, http.MethodPost, "/accounts/"+accountID+"/cfd_tunnel", nil, map[string]any{"name": name, "config_src": "cloudflare"}, &t)
	return t, err
}

// RenameTunnel changes a tunnel's name.
func (c *Client) RenameTunnel(ctx context.Context, accountID, tunnelID, name string) (Tunnel, error) {
	var t Tunnel
	if err := validID("account", accountID); err != nil {
		return t, err
	}
	if err := validID("tunnel", tunnelID); err != nil {
		return t, err
	}
	if err := ValidTunnelName(name); err != nil {
		return t, err
	}
	_, err := c.do(ctx, http.MethodPatch, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID, nil, map[string]any{"name": name}, &t)
	return t, err
}

// DeleteTunnel deletes a tunnel. Cloudflare refuses while connectors are connected.
func (c *Client) DeleteTunnel(ctx context.Context, accountID, tunnelID string) error {
	if err := validID("account", accountID); err != nil {
		return err
	}
	if err := validID("tunnel", tunnelID); err != nil {
		return err
	}
	_, err := c.do(ctx, http.MethodDelete, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID, nil, nil, nil)
	return err
}

// TunnelToken returns the token a connector runs with. Callers must never
// return it to a browser, store it in plain text, or log it.
func (c *Client) TunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	if err := validID("account", accountID); err != nil {
		return "", err
	}
	if err := validID("tunnel", tunnelID); err != nil {
		return "", err
	}
	var tok string
	_, err := c.do(ctx, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/token", nil, nil, &tok)
	return tok, err
}

// Connectors lists the cloudflared processes connected to a tunnel.
func (c *Client) Connectors(ctx context.Context, accountID, tunnelID string) ([]Connector, error) {
	if err := validID("account", accountID); err != nil {
		return nil, err
	}
	if err := validID("tunnel", tunnelID); err != nil {
		return nil, err
	}
	var out []Connector
	_, err := c.do(ctx, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/connections", nil, nil, &out)
	return out, err
}

// Config reads a remotely managed tunnel's configuration.
func (c *Client) Config(ctx context.Context, accountID, tunnelID string) (TunnelConfig, error) {
	var cfg TunnelConfig
	if err := validID("account", accountID); err != nil {
		return cfg, err
	}
	if err := validID("tunnel", tunnelID); err != nil {
		return cfg, err
	}
	_, err := c.do(ctx, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/configurations", nil, nil, &cfg)
	return cfg, err
}

// PutConfig replaces a remotely managed tunnel's configuration. The whole
// config is sent; callers read, edit and write with ApplyConfig.
func (c *Client) PutConfig(ctx context.Context, accountID, tunnelID string, body ConfigBody) (TunnelConfig, error) {
	var cfg TunnelConfig
	if err := validID("account", accountID); err != nil {
		return cfg, err
	}
	if err := validID("tunnel", tunnelID); err != nil {
		return cfg, err
	}
	if err := ValidateIngress(body.Ingress); err != nil {
		return cfg, err
	}
	_, err := c.do(ctx, http.MethodPut, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/configurations", nil, map[string]any{"config": body}, &cfg)
	return cfg, err
}

// ErrConfigChanged means the tunnel config changed between read and write.
var ErrConfigChanged = errors.New("the tunnel's routes changed in Cloudflare while this edit was being made; reload and try again")

// ApplyConfig reads the tunnel config, applies edit, and writes it back. If
// the version moved while editing, nothing is written. It returns the
// config before and after so callers can undo.
func (c *Client) ApplyConfig(ctx context.Context, accountID, tunnelID string, edit func(*ConfigBody) error) (before, after TunnelConfig, err error) {
	before, err = c.Config(ctx, accountID, tunnelID)
	if err != nil {
		return before, after, err
	}
	if before.Source != "" && before.Source != "cloudflare" {
		return before, after, fmt.Errorf("this tunnel is managed by a config file on its connector, so its routes cannot be edited through the API")
	}
	next := cloneBody(before.Config)
	if err := edit(&next); err != nil {
		return before, after, err
	}
	again, err := c.Config(ctx, accountID, tunnelID)
	if err != nil {
		return before, after, err
	}
	if again.Version != before.Version {
		return before, after, ErrConfigChanged
	}
	after, err = c.PutConfig(ctx, accountID, tunnelID, next)
	return before, after, err
}

func cloneBody(b ConfigBody) ConfigBody {
	out := ConfigBody{OriginRequest: b.OriginRequest, WarpRouting: b.WarpRouting}
	out.Ingress = append([]IngressRule(nil), b.Ingress...)
	return out
}

// DNSRecords lists records in a zone named exactly name.
func (c *Client) DNSRecords(ctx context.Context, zoneID, name string) ([]DNSRecord, error) {
	if err := validID("zone", zoneID); err != nil {
		return nil, err
	}
	q := url.Values{}
	if name != "" {
		q.Set("name", strings.ToLower(name))
	}
	return paged[DNSRecord](ctx, c, "/zones/"+zoneID+"/dns_records", q, 100)
}

// CreateDNS creates a record.
func (c *Client) CreateDNS(ctx context.Context, zoneID string, rec DNSRecord) (DNSRecord, error) {
	var out DNSRecord
	if err := validID("zone", zoneID); err != nil {
		return out, err
	}
	rec.ID, rec.ZoneID = "", ""
	if rec.TTL == 0 {
		rec.TTL = 1
	}
	_, err := c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", nil, rec, &out)
	return out, err
}

// UpdateDNS replaces a record's content and proxy setting.
func (c *Client) UpdateDNS(ctx context.Context, zoneID, recordID string, rec DNSRecord) (DNSRecord, error) {
	var out DNSRecord
	if err := validID("zone", zoneID); err != nil {
		return out, err
	}
	if err := validID("record", recordID); err != nil {
		return out, err
	}
	body := map[string]any{"type": rec.Type, "name": rec.Name, "content": rec.Content, "proxied": rec.Proxied, "ttl": 1}
	if rec.Comment != "" {
		body["comment"] = rec.Comment
	}
	_, err := c.do(ctx, http.MethodPatch, "/zones/"+zoneID+"/dns_records/"+recordID, nil, body, &out)
	return out, err
}

// DeleteDNS removes a record.
func (c *Client) DeleteDNS(ctx context.Context, zoneID, recordID string) error {
	if err := validID("zone", zoneID); err != nil {
		return err
	}
	if err := validID("record", recordID); err != nil {
		return err
	}
	_, err := c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+recordID, nil, nil, nil)
	return err
}

var tunnelNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// ValidTunnelName accepts short names of letters, digits, dot, dash and underscore.
func ValidTunnelName(name string) error {
	if !tunnelNameRe.MatchString(name) {
		return fmt.Errorf("tunnel name must be 1-63 letters, digits, dots, dashes or underscores")
	}
	return nil
}
