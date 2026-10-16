package cloudflare_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cf "github.com/no-dal/ndl-ce/internal/cloudflare"
	"github.com/no-dal/ndl-ce/internal/cloudflare/cftest"
)

const acctA, acctB = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func fixture(t *testing.T) *cftest.Fake {
	t.Helper()
	f := cftest.New()
	t.Cleanup(f.Server.Close)
	f.Accounts[acctA], f.Accounts[acctB] = "Racing", "Hosting"
	all := []string{cftest.TunnelRead, cftest.TunnelWrite, cftest.ZoneRead, cftest.DNSRead, cftest.DNSWrite, cftest.AccountList}
	f.Tokens["user-a"] = cftest.Token{Status: "active", Accounts: []string{acctA}, Perms: all}
	f.Tokens["acct-b"] = cftest.Token{Status: "active", AccountOwned: true, Accounts: []string{acctB}, Perms: all}
	f.Tokens["readonly"] = cftest.Token{Status: "active", Accounts: []string{acctA}, Perms: []string{cftest.TunnelRead, cftest.ZoneRead, cftest.DNSRead, cftest.AccountList}}
	f.Tokens["expired"] = cftest.Token{Status: "expired", Accounts: []string{acctA}}
	return f
}

func client(f *cftest.Fake, tok string) *cf.Client {
	return &cf.Client{Token: tok, BaseURL: f.URL(), MinGap: -1}
}

func TestDiscoverUserAndAccountTokens(t *testing.T) {
	f := fixture(t)
	f.AddZone(acctA, "racing.example")
	f.AddZone(acctB, "hosting.example")
	f.AddTunnel(acctA, "shared")

	sc, err := cf.Discover(context.Background(), client(f, "user-a"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Accounts) != 1 || sc.Accounts[0].Account.ID != acctA || !sc.Accounts[0].Tunnels || !sc.Accounts[0].DNS || len(sc.Accounts[0].Zones) != 1 {
		t.Fatalf("user token scope %+v", sc)
	}
	if _, err := cf.Discover(context.Background(), client(f, "acct-b"), ""); err == nil {
		t.Fatal("an account token without its account ID cannot be verified")
	}
	sc, err = cf.Discover(context.Background(), client(f, "acct-b"), acctB)
	if err != nil || !sc.Token.AccountOwned || len(sc.Accounts) != 1 || sc.Accounts[0].Zones[0].Name != "hosting.example" {
		t.Fatalf("account token scope %+v %v", sc, err)
	}
}

func TestDiscoverReportsMissingPermissions(t *testing.T) {
	f := fixture(t)
	f.Tokens["dns-only"] = cftest.Token{Status: "active", Accounts: []string{acctA}, Perms: []string{cftest.ZoneRead, cftest.DNSRead, cftest.AccountList}}
	f.AddZone(acctA, "racing.example")
	sc, err := cf.Discover(context.Background(), client(f, "dns-only"), "")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Accounts[0].Tunnels || len(sc.Accounts[0].Problems) == 0 || !strings.Contains(sc.Accounts[0].Problems[0], "tunnels") {
		t.Fatalf("missing tunnel permission must be reported: %+v", sc.Accounts[0])
	}
}

func TestErrorsAreClassified(t *testing.T) {
	f := fixture(t)
	_, err := client(f, "expired").Tunnels(context.Background(), acctA)
	if !errors.Is(err, cf.ErrAuth) {
		t.Fatalf("expired token: %v", err)
	}
	_, err = client(f, "user-a").Tunnels(context.Background(), acctB)
	if !errors.Is(err, cf.ErrPermission) {
		t.Fatalf("another account's tunnels must be refused: %v", err)
	}
	_, err = client(f, "readonly").CreateTunnel(context.Background(), acctA, "new")
	if !errors.Is(err, cf.ErrPermission) {
		t.Fatalf("read-only token creating a tunnel: %v", err)
	}
	f.FailNext, f.FailStatus = 1, 429
	_, err = client(f, "user-a").Tunnels(context.Background(), acctA)
	var ae *cf.APIError
	if !errors.Is(err, cf.ErrRateLimited) || !errors.As(err, &ae) || ae.RetryAfter != 42*time.Second {
		t.Fatalf("rate limit: %v", err)
	}
	f.FailNext, f.FailStatus = 1, 503
	if _, err = client(f, "user-a").Tunnels(context.Background(), acctA); !errors.Is(err, cf.ErrUnavailable) {
		t.Fatalf("outage: %v", err)
	}
	if _, err = (&cf.Client{BaseURL: f.URL()}).Tunnels(context.Background(), acctA); !errors.Is(err, cf.ErrAuth) {
		t.Fatalf("no token: %v", err)
	}
	if _, err = client(f, "user-a").Tunnels(context.Background(), "../zones"); err == nil {
		t.Fatal("ids must be validated before building a path")
	}
}

func TestApplyConfigKeepsOtherRoutesAndDetectsRaces(t *testing.T) {
	f := fixture(t)
	tun := f.AddTunnel(acctA, "shared",
		map[string]any{"hostname": "a.racing.example", "service": "http://10.0.0.2:80", "originRequest": map[string]any{"noTLSVerify": true, "proxyType": ""}},
		map[string]any{"hostname": "b.racing.example", "service": "http://10.0.0.3:80"})
	c := client(f, "user-a")
	_, _, err := c.ApplyConfig(context.Background(), acctA, tun.ID, func(b *cf.ConfigBody) error {
		_, err := cf.UpsertRule(b, cf.IngressRule{Hostname: "c.racing.example", Service: "https://10.0.0.4:443"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ing := f.Ingress(tun.ID)
	if len(ing) != 4 || ing[2]["hostname"] != "c.racing.example" || ing[3]["service"] != "http_status:404" {
		t.Fatalf("new route must sit before the catch-all: %v", ing)
	}
	if or, _ := ing[0]["originRequest"].(map[string]any); or["noTLSVerify"] != true || or["proxyType"] != "" {
		t.Fatalf("unmodelled origin settings must survive: %v", ing[0])
	}
	_, _, err = c.ApplyConfig(context.Background(), acctA, tun.ID, func(b *cf.ConfigBody) error {
		f.Mu.Lock()
		f.Tunnels[tun.ID].Version++
		f.Mu.Unlock()
		_, err := cf.UpsertRule(b, cf.IngressRule{Hostname: "d.racing.example", Service: "http://10.0.0.5:80"})
		return err
	})
	if !errors.Is(err, cf.ErrConfigChanged) || len(f.Ingress(tun.ID)) != 4 {
		t.Fatalf("a concurrent change must stop the write: %v", err)
	}
	f.Mu.Lock()
	f.Tunnels[tun.ID].ConfigSrc = "local"
	f.Mu.Unlock()
	if _, _, err = c.ApplyConfig(context.Background(), acctA, tun.ID, func(*cf.ConfigBody) error { return nil }); err == nil || !strings.Contains(err.Error(), "config file") {
		t.Fatalf("locally managed tunnels are read-only: %v", err)
	}
}

func TestIngressEditing(t *testing.T) {
	b := cf.ConfigBody{}
	if _, err := cf.UpsertRule(&b, cf.IngressRule{Hostname: "x.example.com", Service: "http://10.1.1.1:80"}); err != nil {
		t.Fatal(err)
	}
	replaced, err := cf.UpsertRule(&b, cf.IngressRule{Hostname: "x.example.com", Service: "http://10.1.1.2:80"})
	if err != nil || !replaced || len(b.Ingress) != 2 || b.Ingress[0].Service != "http://10.1.1.2:80" {
		t.Fatalf("upsert %+v %v", b.Ingress, err)
	}
	if err := cf.ValidateIngress(b.Ingress); err != nil {
		t.Fatal(err)
	}
	if err := cf.ValidateIngress([]cf.IngressRule{{Hostname: "a.example.com", Service: "http://x"}}); err == nil {
		t.Fatal("a config without a catch-all is invalid")
	}
	if _, ok := cf.RemoveRule(&b, "x.example.com", ""); !ok || len(b.Ingress) != 1 || !b.Ingress[0].CatchAll() {
		t.Fatalf("remove %+v", b.Ingress)
	}
	r := cf.IngressRule{Hostname: "y.example.com", Service: "https://10.0.0.1:443", OriginRequest: map[string]any{"access": map[string]any{"required": true}}}
	r = r.WithOptions(cf.OriginOptions{NoTLSVerify: true, HTTPHostHeader: "internal", ConnectTimeoutSeconds: 10})
	if r.OriginRequest["access"] == nil || r.Options().HTTPHostHeader != "internal" || !r.Options().NoTLSVerify {
		t.Fatalf("options %+v", r.OriginRequest)
	}
}

func TestServiceAndHostnameValidation(t *testing.T) {
	if s, err := cf.BuildService("http", "192.168.2.38", 3000); err != nil || s != "http://192.168.2.38:3000" {
		t.Fatal(s, err)
	}
	if s, err := cf.BuildService("https", "fd00::5", 0); err != nil || s != "https://[fd00::5]:443" {
		t.Fatal(s, err)
	}
	for _, bad := range []struct{ proto, host string }{{"unix", "/run/ndl/control.sock"}, {"http", "169.254.169.254"}, {"http", "0.0.0.0"}, {"http", "a b"}, {"ftp", "10.0.0.1"}} {
		if _, err := cf.BuildService(bad.proto, bad.host, 80); err == nil {
			t.Fatalf("%v must be refused", bad)
		}
	}
	if h, err := cf.NormalizeHostname(" App.Example.COM. "); err != nil || h != "app.example.com" {
		t.Fatal(h, err)
	}
	if _, err := cf.NormalizeHostname("*.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"localhost", "-a.example.com", "a..example.com", "http://x.example.com", ""} {
		if _, err := cf.NormalizeHostname(bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}

func TestPlanDNS(t *testing.T) {
	tun := "11111111-2222-3333-4444-555555555555"
	other := "99999999-2222-3333-4444-555555555555"
	cases := []struct {
		recs []cf.DNSRecord
		want string
	}{
		{nil, cf.DNSCreate},
		{[]cf.DNSRecord{{Name: "a.example.com", Type: "CNAME", Content: tun + ".cfargotunnel.com", Proxied: true}}, cf.DNSOK},
		{[]cf.DNSRecord{{Name: "a.example.com", Type: "CNAME", Content: tun + ".cfargotunnel.com"}}, cf.DNSProxy},
		{[]cf.DNSRecord{{Name: "a.example.com", Type: "CNAME", Content: other + ".cfargotunnel.com", Proxied: true}}, cf.DNSRetarget},
		{[]cf.DNSRecord{{Name: "a.example.com", Type: "A", Content: "203.0.113.1"}}, cf.DNSConflict},
		{[]cf.DNSRecord{{Name: "a.example.com", Type: "CNAME", Content: "shop.vendor.example"}}, cf.DNSConflict},
		{[]cf.DNSRecord{{Name: "a.example.com", Type: "TXT", Content: "v=spf1"}}, cf.DNSConflict},
		{[]cf.DNSRecord{{Name: "b.example.com", Type: "A", Content: "203.0.113.1"}}, cf.DNSCreate},
	}
	for i, c := range cases {
		if got := cf.PlanDNS("a.example.com", tun, c.recs); got.Action != c.want {
			t.Fatalf("case %d: %s, want %s (%s)", i, got.Action, c.want, got.Reason)
		}
	}
	z, ok := cf.ZoneFor("app.eu.example.com", []cf.Zone{{Name: "example.com"}, {Name: "eu.example.com"}, {Name: "other.com"}})
	if !ok || z.Name != "eu.example.com" {
		t.Fatalf("longest zone: %+v", z)
	}
	if _, ok := cf.ZoneFor("app.example.org", []cf.Zone{{Name: "example.com"}}); ok {
		t.Fatal("no zone must match")
	}
}

func TestSealerEncryptsAndBindsToID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "cloudflare.key")
	s := &cf.Sealer{Path: path}
	sealed, err := s.Seal("cred-1", "secret-token-value")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "secret-token-value") {
		t.Fatal("sealed value must not contain the token")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", st.Mode())
	}
	got, err := (&cf.Sealer{Path: path}).Open("cred-1", sealed)
	if err != nil || got != "secret-token-value" {
		t.Fatal(got, err)
	}
	if _, err := s.Open("cred-2", sealed); err == nil {
		t.Fatal("a ciphertext must not open for another credential")
	}
	if _, err := (&cf.Sealer{Path: filepath.Join(dir, "other.key")}).Open("cred-1", sealed); err == nil {
		t.Fatal("a different key must not open it")
	}
	if cf.Last4("abcdefgh1234") != "1234" {
		t.Fatal("last4")
	}
}

func TestPagination(t *testing.T) {
	f := fixture(t)
	for i := 0; i < 3; i++ {
		f.AddTunnel(acctA, "t"+string(rune('a'+i)))
	}
	ts, err := client(f, "user-a").Tunnels(context.Background(), acctA)
	if err != nil || len(ts) != 3 {
		t.Fatal(len(ts), err)
	}
}
