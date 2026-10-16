package cloudflare

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// IngressRule is one route in a tunnel's config. OriginRequest keeps every
// field Cloudflare returned, including ones this package does not model.
type IngressRule struct {
	Hostname      string         `json:"hostname,omitempty"`
	Path          string         `json:"path,omitempty"`
	Service       string         `json:"service"`
	OriginRequest map[string]any `json:"originRequest,omitempty"`
}

// CatchAll reports whether the rule matches every request (the last rule).
func (r IngressRule) CatchAll() bool { return r.Hostname == "" && r.Path == "" }

// OriginOptions are the origin settings the UI edits. Zero values are left
// out so Cloudflare's defaults apply.
type OriginOptions struct {
	NoTLSVerify            bool   `json:"no_tls_verify,omitempty"`
	OriginServerName       string `json:"origin_server_name,omitempty"`
	HTTPHostHeader         string `json:"http_host_header,omitempty"`
	HTTP2Origin            bool   `json:"http2_origin,omitempty"`
	DisableChunkedEncoding bool   `json:"disable_chunked_encoding,omitempty"`
	ConnectTimeoutSeconds  int    `json:"connect_timeout_seconds,omitempty"`
}

var originKeys = map[string]string{
	"noTLSVerify": "no_tls_verify", "originServerName": "origin_server_name", "httpHostHeader": "http_host_header",
	"http2Origin": "http2_origin", "disableChunkedEncoding": "disable_chunked_encoding", "connectTimeout": "connect_timeout_seconds",
}

// Options reads the modelled origin settings from a rule.
func (r IngressRule) Options() OriginOptions {
	var o OriginOptions
	or := r.OriginRequest
	if or == nil {
		return o
	}
	o.NoTLSVerify, _ = or["noTLSVerify"].(bool)
	o.OriginServerName, _ = or["originServerName"].(string)
	o.HTTPHostHeader, _ = or["httpHostHeader"].(string)
	o.HTTP2Origin, _ = or["http2Origin"].(bool)
	o.DisableChunkedEncoding, _ = or["disableChunkedEncoding"].(bool)
	switch v := or["connectTimeout"].(type) {
	case float64:
		o.ConnectTimeoutSeconds = int(v)
	case json.Number:
		n, _ := v.Int64()
		o.ConnectTimeoutSeconds = int(n)
	case int:
		o.ConnectTimeoutSeconds = v
	}
	return o
}

// WithOptions sets the modelled origin settings and keeps any others.
func (r IngressRule) WithOptions(o OriginOptions) IngressRule {
	or := map[string]any{}
	for k, v := range r.OriginRequest {
		if _, modelled := originKeys[k]; !modelled {
			or[k] = v
		}
	}
	if o.NoTLSVerify {
		or["noTLSVerify"] = true
	}
	if s := strings.TrimSpace(o.OriginServerName); s != "" {
		or["originServerName"] = s
	}
	if s := strings.TrimSpace(o.HTTPHostHeader); s != "" {
		or["httpHostHeader"] = s
	}
	if o.HTTP2Origin {
		or["http2Origin"] = true
	}
	if o.DisableChunkedEncoding {
		or["disableChunkedEncoding"] = true
	}
	if o.ConnectTimeoutSeconds > 0 {
		or["connectTimeout"] = o.ConnectTimeoutSeconds
	}
	if len(or) == 0 {
		or = nil
	}
	r.OriginRequest = or
	return r
}

var hostnameRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// NormalizeHostname lower-cases and validates a public hostname. A leading
// "*." wildcard is allowed.
func NormalizeHostname(h string) (string, error) {
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	if len(h) > 253 || !hostnameRe.MatchString(h) {
		return "", fmt.Errorf("%q is not a valid public hostname", h)
	}
	return h, nil
}

// Origin protocols a route created in No-dal may use. unix sockets are not
// offered: a socket path on the connector could expose local control sockets.
var originSchemes = map[string]int{"http": 80, "https": 443, "tcp": 0, "ssh": 22, "rdp": 3389, "smb": 445}

// knownSchemes are accepted when reading existing rules.
var knownSchemes = map[string]bool{"http": true, "https": true, "tcp": true, "ssh": true, "rdp": true, "smb": true, "unix": true, "unix+tls": true, "ws": true, "wss": true}

// BuildService makes a service URL such as http://10.0.0.5:8080 from parts.
func BuildService(protocol, host string, port int) (string, error) {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	def, ok := originSchemes[protocol]
	if !ok {
		return "", fmt.Errorf("origin protocol must be http, https, tcp, ssh, rdp or smb")
	}
	host = strings.TrimSpace(host)
	if err := ValidOriginHost(host); err != nil {
		return "", err
	}
	if port == 0 {
		port = def
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("origin port must be between 1 and 65535")
	}
	return protocol + "://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

var originNameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// ValidOriginHost accepts an IP address or a DNS name. Link-local addresses
// (including cloud metadata endpoints) and unspecified addresses are refused.
func ValidOriginHost(host string) error {
	if host == "" {
		return fmt.Errorf("origin address is required")
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return fmt.Errorf("origin address %s is not allowed", host)
		}
		return nil
	}
	if !originNameRe.MatchString(host) {
		return fmt.Errorf("origin address %q is not an IP address or host name", host)
	}
	return nil
}

// ParsedService is a service URL split into parts.
type ParsedService struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

// ParseService splits a service such as http://10.0.0.5:8080. Special
// services (http_status:404, hello_world) return ok=false.
func ParseService(service string) (ParsedService, bool) {
	if !strings.Contains(service, "://") {
		return ParsedService{}, false
	}
	u, err := url.Parse(service)
	if err != nil || u.Host == "" {
		return ParsedService{}, false
	}
	p := ParsedService{Protocol: strings.ToLower(u.Scheme), Host: u.Hostname()}
	p.Port, _ = strconv.Atoi(u.Port())
	if p.Port == 0 {
		p.Port = originSchemes[p.Protocol]
	}
	return p, true
}

func validService(s string) error {
	switch {
	case s == "":
		return fmt.Errorf("every route needs a service")
	case s == "hello_world" || strings.HasPrefix(s, "bastion"):
		return nil
	case strings.HasPrefix(s, "http_status:"):
		code, err := strconv.Atoi(strings.TrimPrefix(s, "http_status:"))
		if err != nil || code < 100 || code > 599 {
			return fmt.Errorf("%q is not a valid status service", s)
		}
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || !knownSchemes[strings.ToLower(u.Scheme)] {
		return fmt.Errorf("service %q has an unsupported protocol", s)
	}
	return nil
}

// ValidateIngress checks a full rule list: every rule but the last has a
// hostname, the last matches everything, and no hostname+path repeats.
func ValidateIngress(rules []IngressRule) error {
	if len(rules) == 0 {
		return fmt.Errorf("a tunnel config needs at least a catch-all rule")
	}
	seen := map[string]bool{}
	for i, r := range rules {
		if err := validService(r.Service); err != nil {
			return err
		}
		last := i == len(rules)-1
		if last {
			if !r.CatchAll() {
				return fmt.Errorf("the last route must be a catch-all with no hostname")
			}
			continue
		}
		if r.Hostname == "" {
			return fmt.Errorf("only the last route may omit its hostname")
		}
		key := r.Hostname + "|" + r.Path
		if seen[key] {
			return fmt.Errorf("%s%s is routed twice", r.Hostname, r.Path)
		}
		seen[key] = true
	}
	return nil
}

// DefaultCatchAll answers unmatched requests with 404.
var DefaultCatchAll = IngressRule{Service: "http_status:404"}

func ensureCatchAll(b *ConfigBody) {
	if n := len(b.Ingress); n == 0 || !b.Ingress[n-1].CatchAll() {
		b.Ingress = append(b.Ingress, DefaultCatchAll)
	}
}

// FindRule returns the rule for hostname and path.
func FindRule(b ConfigBody, hostname, path string) (IngressRule, int) {
	for i, r := range b.Ingress {
		if !r.CatchAll() && strings.EqualFold(r.Hostname, hostname) && r.Path == path {
			return r, i
		}
	}
	return IngressRule{}, -1
}

// UpsertRule replaces the rule with the same hostname and path, or adds it
// ahead of the catch-all. It reports whether a rule was replaced.
func UpsertRule(b *ConfigBody, r IngressRule) (bool, error) {
	if r.CatchAll() {
		return false, fmt.Errorf("a route needs a hostname")
	}
	if err := validService(r.Service); err != nil {
		return false, err
	}
	ensureCatchAll(b)
	if _, i := FindRule(*b, r.Hostname, r.Path); i >= 0 {
		b.Ingress[i] = r
		return true, nil
	}
	n := len(b.Ingress)
	b.Ingress = append(b.Ingress[:n-1], r, b.Ingress[n-1])
	return false, nil
}

// RemoveRule removes the rule for hostname and path.
func RemoveRule(b *ConfigBody, hostname, path string) (IngressRule, bool) {
	r, i := FindRule(*b, hostname, path)
	if i < 0 {
		return r, false
	}
	b.Ingress = append(b.Ingress[:i], b.Ingress[i+1:]...)
	ensureCatchAll(b)
	return r, true
}
