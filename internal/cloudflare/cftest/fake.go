// Package cftest is an in-memory fake of the Cloudflare API endpoints No-dal
// uses. Each token sees only the accounts and permissions it is given, so
// tests can prove multi-account isolation and permission handling.
package cftest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Permissions a fake token can hold.
const (
	TunnelRead  = "tunnel:read"
	TunnelWrite = "tunnel:write"
	ZoneRead    = "zone:read"
	DNSRead     = "dns:read"
	DNSWrite    = "dns:write"
	AccountList = "account:list"
)

// Token is a fake API token.
type Token struct {
	Status       string // active, expired, disabled
	AccountOwned bool
	Accounts     []string
	Perms        []string
}

func (t Token) has(p string) bool {
	for _, x := range t.Perms {
		if x == p {
			return true
		}
	}
	return false
}

func (t Token) sees(account string) bool {
	for _, a := range t.Accounts {
		if a == account {
			return true
		}
	}
	return false
}

// Tunnel is a fake tunnel with its config and connectors.
type Tunnel struct {
	ID         string
	Account    string
	Name       string
	ConfigSrc  string
	Status     string
	Version    int
	Config     map[string]any
	Connectors []map[string]any
	Deleted    bool
	Token      string
}

// Record is a fake DNS record.
type Record struct {
	ID, Zone, Name, Type, Content, Comment string
	Proxied                                bool
}

// Fake is the fake API. Lock Mu when changing state from a test.
type Fake struct {
	Mu       sync.Mutex
	Server   *httptest.Server
	Tokens   map[string]Token
	Accounts map[string]string            // id -> name
	Zones    map[string]map[string]string // id -> {name, account}
	Tunnels  map[string]*Tunnel
	Records  map[string]*Record
	// Calls counts requests by "METHOD path".
	Calls map[string]int
	// Refused lists "METHOD path" calls made with a token that cannot see
	// the account or zone they name.
	Refused []string
	// FailNext makes the next n requests return this status.
	FailNext   int
	FailStatus int
	// BeforeWrite runs before a config PUT is applied (to simulate races).
	BeforeWrite func(t *Tunnel)
}

// New starts a fake. Close it with f.Server.Close().
func New() *Fake {
	f := &Fake{
		Tokens: map[string]Token{}, Accounts: map[string]string{}, Zones: map[string]map[string]string{},
		Tunnels: map[string]*Tunnel{}, Records: map[string]*Record{}, Calls: map[string]int{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// URL is the base URL to give the client.
func (f *Fake) URL() string { return f.Server.URL + "/client/v4" }

// AddZone adds a zone to an account and returns its ID.
func (f *Fake) AddZone(account, name string) string {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	f.Zones[id] = map[string]string{"name": name, "account": account}
	return id
}

// AddTunnel adds a remotely managed tunnel with routes and returns it.
func (f *Fake) AddTunnel(account, name string, ingress ...map[string]any) *Tunnel {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	ing := []any{}
	for _, r := range ingress {
		ing = append(ing, r)
	}
	ing = append(ing, map[string]any{"service": "http_status:404"})
	t := &Tunnel{ID: uuid.NewString(), Account: account, Name: name, ConfigSrc: "cloudflare", Status: "healthy", Version: 1,
		Config: map[string]any{"ingress": ing}, Token: "eyFAKE-" + uuid.NewString(),
		Connectors: []map[string]any{{"id": uuid.NewString(), "version": "2026.8.3", "arch": "linux_amd64", "conns": []any{map[string]any{"colo_name": "syd01", "origin_ip": "203.0.113.9"}}}}}
	f.Tunnels[t.ID] = t
	return t
}

// AddRecord adds a DNS record.
func (f *Fake) AddRecord(zone, name, typ, content string, proxied bool) *Record {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	r := &Record{ID: strings.ReplaceAll(uuid.NewString(), "-", ""), Zone: zone, Name: name, Type: typ, Content: content, Proxied: proxied}
	f.Records[r.ID] = r
	return r
}

// Ingress returns a tunnel's current rules.
func (f *Fake) Ingress(tunnelID string) []map[string]any {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []map[string]any
	for _, r := range f.Tunnels[tunnelID].Config["ingress"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out
}

// RecordsNamed returns records with a name.
func (f *Fake) RecordsNamed(name string) []Record {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []Record
	for _, r := range f.Records {
		if r.Name == name {
			out = append(out, *r)
		}
	}
	return out
}

func write(w http.ResponseWriter, status int, result any, info map[string]any, errs ...map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"success": status < 300, "errors": errs, "messages": []any{}, "result": result}
	if errs == nil {
		body["errors"] = []any{}
	}
	if info != nil {
		body["result_info"] = info
	}
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, status, code int, msg string) {
	write(w, status, nil, nil, map[string]any{"code": code, "message": msg})
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	f.Calls[r.Method+" "+path]++
	if f.FailNext > 0 {
		f.FailNext--
		if f.FailStatus == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "42")
			fail(w, 429, 10429, "Rate limited")
			return
		}
		fail(w, f.FailStatus, 10001, "service unavailable")
		return
	}
	tokStr := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	tok, ok := f.Tokens[tokStr]
	if !ok || tok.Status == "expired" || tok.Status == "disabled" {
		if ok && strings.HasSuffix(path, "/tokens/verify") {
			write(w, 200, map[string]any{"id": "tok", "status": tok.Status}, nil)
			return
		}
		fail(w, 401, 10000, "Authentication error")
		return
	}
	need := func(p string) bool {
		if !tok.has(p) {
			fail(w, 403, 10000, "Authentication error")
			return false
		}
		return true
	}
	switch {
	case path == "/user/tokens/verify":
		if tok.AccountOwned {
			fail(w, 401, 1000, "Invalid API Token")
			return
		}
		write(w, 200, map[string]any{"id": "tok", "status": "active"}, nil)
	case len(parts) == 4 && parts[0] == "accounts" && parts[2] == "tokens" && parts[3] == "verify":
		if !tok.AccountOwned || !tok.sees(parts[1]) {
			fail(w, 401, 1000, "Invalid API Token")
			return
		}
		write(w, 200, map[string]any{"id": "tok", "status": "active"}, nil)
	case path == "/accounts":
		if tok.AccountOwned || !tok.has(AccountList) {
			write(w, 200, []any{}, map[string]any{"page": 1, "count": 0, "total_count": 0})
			return
		}
		var out []any
		for _, a := range tok.Accounts {
			out = append(out, map[string]any{"id": a, "name": f.Accounts[a]})
		}
		write(w, 200, out, map[string]any{"page": 1, "count": len(out), "total_count": len(out)})
	case len(parts) == 2 && parts[0] == "accounts":
		if !tok.sees(parts[1]) {
			f.Refused = append(f.Refused, r.Method+" "+r.URL.Path)
			fail(w, 403, 9109, "Unauthorized to access requested resource")
			return
		}
		write(w, 200, map[string]any{"id": parts[1], "name": f.Accounts[parts[1]]}, nil)
	case path == "/zones":
		acct := r.URL.Query().Get("account.id")
		if !tok.sees(acct) || !need(ZoneRead) {
			if !tok.sees(acct) {
				write(w, 200, []any{}, map[string]any{"page": 1, "count": 0})
			}
			return
		}
		var out []any
		for id, z := range f.Zones {
			if z["account"] == acct {
				out = append(out, map[string]any{"id": id, "name": z["name"], "status": "active", "account": map[string]any{"id": acct, "name": f.Accounts[acct]}})
			}
		}
		write(w, 200, out, map[string]any{"page": 1, "count": len(out), "total_count": len(out)})
	case len(parts) >= 2 && parts[0] == "zones":
		f.serveDNS(w, r, tok, parts, need)
	case len(parts) >= 3 && parts[0] == "accounts" && parts[2] == "cfd_tunnel":
		f.serveTunnels(w, r, tok, parts, need)
	default:
		fail(w, 404, 7003, "Could not route to "+path)
	}
}

func (f *Fake) serveTunnels(w http.ResponseWriter, r *http.Request, tok Token, parts []string, need func(string) bool) {
	acct := parts[1]
	if !tok.sees(acct) {
		f.Refused = append(f.Refused, r.Method+" "+r.URL.Path)
		fail(w, 403, 9109, "Unauthorized to access requested resource")
		return
	}
	if len(parts) == 3 {
		switch r.Method {
		case http.MethodGet:
			if !need(TunnelRead) {
				return
			}
			var out []any
			for _, t := range f.Tunnels {
				if t.Account == acct && !t.Deleted {
					out = append(out, tunnelJSON(t))
				}
			}
			write(w, 200, out, map[string]any{"page": 1, "count": len(out), "total_count": len(out)})
		case http.MethodPost:
			if !need(TunnelWrite) {
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["name"].(string)
			for _, t := range f.Tunnels {
				if t.Account == acct && t.Name == name && !t.Deleted {
					fail(w, 409, 1013, "You already have a tunnel with this name")
					return
				}
			}
			t := &Tunnel{ID: uuid.NewString(), Account: acct, Name: name, ConfigSrc: "cloudflare", Status: "inactive", Version: 0,
				Config: map[string]any{"ingress": []any{map[string]any{"service": "http_status:404"}}}, Token: "eyFAKE-" + uuid.NewString()}
			f.Tunnels[t.ID] = t
			write(w, 200, tunnelJSON(t), nil)
		}
		return
	}
	t := f.Tunnels[parts[3]]
	if t == nil || t.Account != acct || t.Deleted {
		fail(w, 404, 1003, "Tunnel not found")
		return
	}
	sub := ""
	if len(parts) > 4 {
		sub = parts[4]
	}
	switch {
	case sub == "" && r.Method == http.MethodPatch:
		if !need(TunnelWrite) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if n, ok := body["name"].(string); ok {
			t.Name = n
		}
		write(w, 200, tunnelJSON(t), nil)
	case sub == "" && r.Method == http.MethodDelete:
		if !need(TunnelWrite) {
			return
		}
		if len(t.Connectors) > 0 {
			fail(w, 400, 1022, "Cannot delete tunnel because it has active connections")
			return
		}
		t.Deleted = true
		write(w, 200, tunnelJSON(t), nil)
	case sub == "token":
		if !need(TunnelWrite) {
			return
		}
		write(w, 200, t.Token, nil)
	case sub == "connections":
		if !need(TunnelRead) {
			return
		}
		write(w, 200, t.Connectors, nil)
	case sub == "configurations" && r.Method == http.MethodGet:
		if !need(TunnelRead) {
			return
		}
		write(w, 200, map[string]any{"tunnel_id": t.ID, "version": t.Version, "source": t.ConfigSrc, "config": t.Config}, nil)
	case sub == "configurations" && r.Method == http.MethodPut:
		if !need(TunnelWrite) {
			return
		}
		if t.ConfigSrc != "cloudflare" {
			fail(w, 400, 1055, "Tunnel is locally configured")
			return
		}
		var body struct {
			Config map[string]any `json:"config"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if f.BeforeWrite != nil {
			f.BeforeWrite(t)
		}
		t.Config = body.Config
		t.Version++
		write(w, 200, map[string]any{"tunnel_id": t.ID, "version": t.Version, "source": "cloudflare", "config": t.Config}, nil)
	default:
		fail(w, 404, 7003, "no route")
	}
}

func tunnelJSON(t *Tunnel) map[string]any {
	now := time.Now().UTC().Format(time.RFC3339)
	return map[string]any{"id": t.ID, "name": t.Name, "account_tag": t.Account, "status": t.Status, "config_src": t.ConfigSrc,
		"remote_config": t.ConfigSrc == "cloudflare", "tun_type": "cfd_tunnel", "created_at": now, "connections": []any{}}
}

func (f *Fake) serveDNS(w http.ResponseWriter, r *http.Request, tok Token, parts []string, need func(string) bool) {
	zone := f.Zones[parts[1]]
	if zone != nil && !tok.sees(zone["account"]) {
		f.Refused = append(f.Refused, r.Method+" "+r.URL.Path)
	}
	if zone == nil || !tok.sees(zone["account"]) {
		fail(w, 403, 9109, "Unauthorized to access requested resource")
		return
	}
	if len(parts) < 3 || parts[2] != "dns_records" {
		fail(w, 404, 7003, "no route")
		return
	}
	switch {
	case len(parts) == 3 && r.Method == http.MethodGet:
		if !need(DNSRead) && !tok.has(DNSWrite) {
			return
		}
		name := r.URL.Query().Get("name")
		var out []any
		for _, rec := range f.Records {
			if rec.Zone == parts[1] && (name == "" || rec.Name == name) {
				out = append(out, recordJSON(rec))
			}
		}
		write(w, 200, out, map[string]any{"page": 1, "count": len(out), "total_count": len(out)})
	case len(parts) == 3 && r.Method == http.MethodPost:
		if !need(DNSWrite) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		name, _ := body["name"].(string)
		typ, _ := body["type"].(string)
		for _, rec := range f.Records {
			if rec.Zone == parts[1] && rec.Name == name && (typ == "CNAME" || rec.Type == "CNAME") {
				fail(w, 400, 81053, "An A, AAAA, or CNAME record with that host already exists.")
				return
			}
		}
		rec := &Record{ID: strings.ReplaceAll(uuid.NewString(), "-", ""), Zone: parts[1], Name: name, Type: typ}
		rec.Content, _ = body["content"].(string)
		rec.Proxied, _ = body["proxied"].(bool)
		rec.Comment, _ = body["comment"].(string)
		f.Records[rec.ID] = rec
		write(w, 200, recordJSON(rec), nil)
	case len(parts) == 4:
		rec := f.Records[parts[3]]
		if rec == nil || rec.Zone != parts[1] {
			fail(w, 404, 81044, "Record does not exist.")
			return
		}
		if !need(DNSWrite) {
			return
		}
		if r.Method == http.MethodDelete {
			delete(f.Records, rec.ID)
			write(w, 200, map[string]any{"id": rec.ID}, nil)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if v, ok := body["content"].(string); ok {
			rec.Content = v
		}
		if v, ok := body["proxied"].(bool); ok {
			rec.Proxied = v
		}
		if v, ok := body["comment"].(string); ok {
			rec.Comment = v
		}
		write(w, 200, recordJSON(rec), nil)
	default:
		fail(w, 404, 7003, fmt.Sprintf("no route %v", parts))
	}
}

func recordJSON(r *Record) map[string]any {
	return map[string]any{"id": r.ID, "zone_id": r.Zone, "name": r.Name, "type": r.Type, "content": r.Content, "proxied": r.Proxied, "ttl": 1, "comment": r.Comment}
}
