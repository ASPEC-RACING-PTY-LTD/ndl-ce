package docker

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reverse proxies (Caddy and Nginx) on the host and in system containers.
//
// No-dal finds them, lists the sites in their configuration and manages sites
// of its own, one file per site in a directory the main configuration
// includes:
//
//	Caddy: /etc/caddy/ndl.d/<name>.caddy, imported from /etc/caddy/Caddyfile
//	Nginx: /etc/nginx/conf.d/ndl-<name>.conf, included by nginx.conf
//
// Sites in other files are shown but never changed. Every change is checked
// with the proxy's own validator before it is loaded, and put back if the
// check or the reload fails.

// Proxy kinds.
const (
	ProxyCaddy = "caddy"
	ProxyNginx = "nginx"
)

// Proxy actions.
const (
	ProxyScan     = "scan"
	ProxyApply    = "apply"
	ProxyRemove   = "remove"
	ProxyReload   = "reload"
	ProxyRestart  = "restart"
	ProxyStart    = "start"
	ProxyStop     = "stop"
	ProxyValidate = "validate"
	ProxyInstall  = "install"
)

// Site TLS modes. Caddy: auto (certificates from a public CA), internal (its
// own CA) or http. Nginx: http or cert (certificate files on the machine).
const (
	ProxyTLSAuto     = "auto"
	ProxyTLSInternal = "internal"
	ProxyTLSHTTP     = "http"
	ProxyTLSCert     = "cert"
)

const proxyMarker = "# Managed by No-dal. Edit this site in No-dal; changes made here are replaced."

// ProxySite is one site: the names it answers and where it sends requests.
type ProxySite struct {
	// Name identifies a managed site's file.
	Name     string   `json:"name,omitempty"`
	Domains  []string `json:"domains"`
	Upstream string   `json:"upstream,omitempty"`
	// Redirect is set for sites that only redirect.
	Redirect string `json:"redirect,omitempty"`
	TLS      string `json:"tls,omitempty"`
	// CertPath and KeyPath are certificate files for Nginx TLS.
	CertPath  string   `json:"cert_path,omitempty"`
	KeyPath   string   `json:"key_path,omitempty"`
	Websocket bool     `json:"websocket,omitempty"`
	Listen    []string `json:"listen,omitempty"`
	Managed   bool     `json:"managed"`
	File      string   `json:"file,omitempty"`
	Line      int      `json:"line,omitempty"`

	upstreamMatched bool
}

// ProxyInstance is Caddy or Nginx on one machine.
type ProxyInstance struct {
	Kind        string      `json:"kind"`
	MachineID   string      `json:"machine_id"`
	MachineName string      `json:"machine_name"`
	Binary      string      `json:"binary"`
	Version     string      `json:"version,omitempty"`
	Service     string      `json:"service"`
	Active      bool        `json:"active"`
	State       string      `json:"state"`
	ConfigPath  string      `json:"config_path"`
	ManagedDir  string      `json:"managed_dir"`
	Included    bool        `json:"included"`
	Sites       []ProxySite `json:"sites"`
	Errors      []string    `json:"errors,omitempty"`
	Notes       []string    `json:"notes,omitempty"`
}

// ProxyRequest asks the agent to act on a proxy.
type ProxyRequest struct {
	Action    string        `json:"action"`
	Kind      string        `json:"kind"`
	MachineID string        `json:"machine_id,omitempty"`
	Site      *ProxySite    `json:"site,omitempty"`
	Previous  string        `json:"previous,omitempty"`
	Name      string        `json:"name,omitempty"`
	Hints     []MachineHint `json:"hints,omitempty"`
}

// ProxyMachine is a machine a proxy could run on.
type ProxyMachine struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	CanApt    bool   `json:"can_apt"`
}

// ProxyResult is the answer to a proxy request.
type ProxyResult struct {
	Instances []ProxyInstance `json:"instances,omitempty"`
	Machines  []ProxyMachine  `json:"machines,omitempty"`
	Message   string          `json:"message,omitempty"`
	Output    string          `json:"output,omitempty"`
	Site      *ProxySite      `json:"site,omitempty"`
}

type proxyKind struct {
	bins       []string
	service    string
	config     string
	confDir    string
	managedDir string
	include    string
}

var proxyKinds = map[string]proxyKind{
	ProxyCaddy: {
		bins: []string{"/usr/bin/caddy", "/usr/local/bin/caddy"}, service: "caddy",
		config: "/etc/caddy/Caddyfile", confDir: "/etc/caddy", managedDir: "/etc/caddy/ndl.d",
		include: "/etc/caddy/ndl.d/*.caddy",
	},
	ProxyNginx: {
		bins: []string{"/usr/sbin/nginx", "/usr/bin/nginx", "/usr/local/sbin/nginx"}, service: "nginx",
		config: "/etc/nginx/nginx.conf", confDir: "/etc/nginx", managedDir: "/etc/nginx/conf.d",
		include: "/etc/nginx/conf.d/*.conf",
	},
}

func (k proxyKind) managedPath(kind, name string) string {
	if kind == ProxyCaddy {
		return k.managedDir + "/" + name + ".caddy"
	}
	return k.managedDir + "/ndl-" + name + ".conf"
}

func managedFile(kind, file string) (string, bool) {
	base := path.Base(file)
	switch kind {
	case ProxyCaddy:
		if path.Dir(file) == proxyKinds[ProxyCaddy].managedDir && strings.HasSuffix(base, ".caddy") {
			return strings.TrimSuffix(base, ".caddy"), true
		}
	case ProxyNginx:
		if path.Dir(file) == proxyKinds[ProxyNginx].managedDir && strings.HasPrefix(base, "ndl-") && strings.HasSuffix(base, ".conf") {
			return strings.TrimSuffix(strings.TrimPrefix(base, "ndl-"), ".conf"), true
		}
	}
	return "", false
}

var (
	proxyNameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,79}$`)
	proxyDomainRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	proxyHostRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	proxyPathRe   = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,255}$`)
)

// ProxySiteName derives a managed file name from a site's first domain.
func ProxySiteName(domain string) string {
	d := strings.ToLower(strings.TrimSpace(domain))
	d = strings.ReplaceAll(d, "*", "wildcard")
	var b strings.Builder
	for _, r := range d {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	n := strings.Trim(b.String(), ".-")
	if len(n) > 80 {
		n = n[:80]
	}
	return n
}

// CleanProxySite checks a site to be written. Every value ends up in a
// configuration file, so each is matched against a strict pattern; nothing
// can add directives of its own.
func CleanProxySite(kind string, s ProxySite) (ProxySite, error) {
	out := ProxySite{TLS: s.TLS, Websocket: s.Websocket, Managed: true}
	seen := map[string]bool{}
	for _, d := range s.Domains {
		d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" || seen[d] {
			continue
		}
		if len(d) > 253 || !proxyDomainRe.MatchString(d) {
			return out, fmt.Errorf("%q is not a valid domain name", d)
		}
		seen[d] = true
		out.Domains = append(out.Domains, d)
	}
	if len(out.Domains) == 0 {
		return out, fmt.Errorf("add at least one domain")
	}
	if len(out.Domains) > 20 {
		return out, fmt.Errorf("a site can answer at most 20 domains")
	}
	up, err := cleanUpstream(s.Upstream)
	if err != nil {
		return out, err
	}
	out.Upstream = up
	switch kind {
	case ProxyCaddy:
		if out.TLS == "" {
			out.TLS = ProxyTLSAuto
		}
		if out.TLS != ProxyTLSAuto && out.TLS != ProxyTLSInternal && out.TLS != ProxyTLSHTTP {
			return out, fmt.Errorf("TLS must be auto, internal or http")
		}
		out.Websocket = false // Caddy proxies WebSockets by itself.
	case ProxyNginx:
		if out.TLS == "" {
			out.TLS = ProxyTLSHTTP
		}
		switch out.TLS {
		case ProxyTLSHTTP:
		case ProxyTLSCert:
			for _, p := range []string{s.CertPath, s.KeyPath} {
				if !proxyPathRe.MatchString(p) || strings.Contains(p, "..") {
					return out, fmt.Errorf("certificate and key must be absolute file paths")
				}
			}
			out.CertPath, out.KeyPath = s.CertPath, s.KeyPath
		default:
			return out, fmt.Errorf("TLS must be http or cert")
		}
	default:
		return out, fmt.Errorf("unknown proxy %q", kind)
	}
	out.Name = s.Name
	if out.Name == "" {
		out.Name = ProxySiteName(out.Domains[0])
	}
	if !proxyNameRe.MatchString(out.Name) {
		return out, fmt.Errorf("site name is invalid")
	}
	return out, nil
}

func cleanUpstream(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("enter where the site sends requests, for example http://10.0.3.12:8080")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("the upstream must look like http://host:port")
	}
	host, port := u.Hostname(), u.Port()
	n, perr := strconv.Atoi(port)
	if perr != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("the upstream needs a port, for example http://10.0.3.12:8080")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
			return "", fmt.Errorf("%s cannot be used as an upstream", host)
		}
		host = ip.String()
	} else if !proxyHostRe.MatchString(strings.ToLower(host)) {
		return "", fmt.Errorf("%q is not a valid upstream host", host)
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(host), port), nil
}

// RenderProxySite writes a managed site's configuration.
func RenderProxySite(kind string, s ProxySite) string {
	var b strings.Builder
	b.WriteString(proxyMarker + "\n")
	switch kind {
	case ProxyCaddy:
		addrs := make([]string, len(s.Domains))
		for i, d := range s.Domains {
			if s.TLS == ProxyTLSHTTP {
				d = "http://" + d
			}
			addrs[i] = d
		}
		b.WriteString(strings.Join(addrs, ", ") + " {\n")
		if s.TLS == ProxyTLSInternal {
			b.WriteString("\ttls internal\n")
		}
		b.WriteString("\treverse_proxy " + s.Upstream)
		if strings.HasPrefix(s.Upstream, "https://") {
			b.WriteString(" {\n\t\theader_up Host {upstream_hostport}\n\t}")
		}
		b.WriteString("\n}\n")
	case ProxyNginx:
		names := strings.Join(s.Domains, " ")
		loc := "\tlocation / {\n" +
			"\t\tproxy_pass " + s.Upstream + ";\n" +
			"\t\tproxy_http_version 1.1;\n" +
			"\t\tproxy_set_header Host $host;\n" +
			"\t\tproxy_set_header X-Real-IP $remote_addr;\n" +
			"\t\tproxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n" +
			"\t\tproxy_set_header X-Forwarded-Proto $scheme;\n"
		if s.Websocket {
			loc += "\t\tproxy_set_header Upgrade $http_upgrade;\n\t\tproxy_set_header Connection \"upgrade\";\n\t\tproxy_read_timeout 1h;\n"
		}
		if strings.HasPrefix(s.Upstream, "https://") {
			loc += "\t\tproxy_ssl_server_name on;\n"
		}
		loc += "\t}\n"
		if s.TLS == ProxyTLSCert {
			b.WriteString("server {\n\tlisten 80;\n\tlisten [::]:80;\n\tserver_name " + names + ";\n\treturn 301 https://$host$request_uri;\n}\n\n")
			b.WriteString("server {\n\tlisten 443 ssl;\n\tlisten [::]:443 ssl;\n\tserver_name " + names + ";\n")
			b.WriteString("\tssl_certificate " + s.CertPath + ";\n\tssl_certificate_key " + s.KeyPath + ";\n")
		} else {
			b.WriteString("server {\n\tlisten 80;\n\tlisten [::]:80;\n\tserver_name " + names + ";\n")
		}
		b.WriteString(loc + "}\n")
	}
	return b.String()
}

// Proxy runs one proxy action.
func (e *Engine) Proxy(ctx context.Context, req ProxyRequest) (ProxyResult, error) {
	k, ok := proxyKinds[req.Kind]
	if !ok {
		return ProxyResult{}, fmt.Errorf("unknown proxy %q", req.Kind)
	}
	if req.Action == ProxyScan {
		return e.proxyScan(ctx, req.Kind, k, req.Hints), nil
	}
	root, err := e.machineRootFor(ctx, req.MachineID)
	if err != nil {
		return ProxyResult{}, err
	}
	switch req.Action {
	case ProxyInstall:
		return e.proxyInstall(ctx, req, k, root)
	}
	bin := findIn(root, k.bins)
	if bin == "" {
		return ProxyResult{}, fmt.Errorf("%s is not installed on this machine", req.Kind)
	}
	switch req.Action {
	case ProxyApply:
		return e.proxyApply(ctx, req, k, root, bin)
	case ProxyRemove:
		return e.proxyRemove(ctx, req, k, root, bin)
	case ProxyValidate:
		out, err := e.proxyValidate(ctx, req.MachineID, req.Kind, k, bin)
		if err != nil {
			return ProxyResult{Output: out}, fmt.Errorf("the configuration has errors: %s", lastLine([]byte(out), err))
		}
		return ProxyResult{Message: "the configuration is valid", Output: out}, nil
	case ProxyReload, ProxyRestart, ProxyStart, ProxyStop:
		if req.Action != ProxyStop {
			if out, err := e.proxyValidate(ctx, req.MachineID, req.Kind, k, bin); err != nil {
				return ProxyResult{Output: out}, fmt.Errorf("not %s: the configuration has errors: %s", map[string]string{ProxyReload: "reloaded", ProxyRestart: "restarted", ProxyStart: "started"}[req.Action], lastLine([]byte(out), err))
			}
		}
		verb := req.Action
		if verb == ProxyStart {
			verb = "start"
		}
		if out, err := e.machineRun(ctx, req.MachineID, binSystemctl, verb, k.service); err != nil {
			return ProxyResult{Output: string(out)}, fmt.Errorf("systemctl %s %s failed: %s", verb, k.service, lastLine(out, err))
		}
		return ProxyResult{Message: k.service + " " + map[string]string{ProxyReload: "reloaded", ProxyRestart: "restarted", ProxyStart: "started", ProxyStop: "stopped"}[req.Action]}, nil
	}
	return ProxyResult{}, fmt.Errorf("unknown proxy action %q", req.Action)
}

func findIn(root string, bins []string) string {
	for _, b := range bins {
		if existsInRoot(root, b) {
			return b
		}
	}
	return ""
}

func (e *Engine) proxyScan(ctx context.Context, kind string, k proxyKind, hints []MachineHint) ProxyResult {
	machines := []MachineHint{{ID: HostMachineID, Name: "Host", Kind: KindHost}}
	machines = append(machines, hints...)
	var res ProxyResult
	for _, m := range machines {
		mctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		root, err := e.machineRootFor(mctx, m.ID)
		if err != nil {
			cancel()
			continue
		}
		pm := ProxyMachine{ID: m.ID, Name: firstNonEmptyStr(m.Name, m.ID), CanApt: existsInRoot(root, binAptGet)}
		if bin := findIn(root, k.bins); bin != "" {
			pm.Installed = true
			res.Instances = append(res.Instances, e.proxyInstance(mctx, kind, k, m, root, bin))
		}
		res.Machines = append(res.Machines, pm)
		cancel()
	}
	return res
}

func (e *Engine) proxyInstance(ctx context.Context, kind string, k proxyKind, m MachineHint, root, bin string) ProxyInstance {
	in := ProxyInstance{Kind: kind, MachineID: m.ID, MachineName: firstNonEmptyStr(m.Name, m.ID), Binary: bin,
		Service: k.service, ConfigPath: k.config, ManagedDir: k.managedDir, Sites: []ProxySite{}}
	verArgs := []string{bin, "version"}
	if kind == ProxyNginx {
		verArgs = []string{bin, "-v"}
	}
	if out, err := e.machineRun(ctx, m.ID, verArgs...); err == nil {
		v := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		in.Version = strings.TrimPrefix(v, "nginx version: ")
	}
	if out, err := e.machineRun(ctx, m.ID, binSystemctl, "show", "-p", "ActiveState", "--value", k.service); err == nil {
		in.State = strings.TrimSpace(string(out))
		in.Active = in.State == "active" || in.State == "reloading"
	}
	if in.State == "" {
		in.State = "unknown"
	}
	sites, included, notes := readProxySites(kind, k, root)
	in.Sites, in.Included, in.Notes = sites, included, notes
	if out, err := e.machineRun(ctx, m.ID, binJournalctl, "-u", k.service, "-p", "warning", "--since", "-30min", "--no-pager", "-o", "cat", "-n", "8"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "-- ") {
				in.Errors = append(in.Errors, shortMsg(l))
			}
		}
	}
	return in
}

// readProxySites reads the main configuration and the files it includes.
func readProxySites(kind string, k proxyKind, root string) ([]ProxySite, bool, []string) {
	var sites []ProxySite
	var notes []string
	seen := map[string]bool{}
	included := false
	files := 0
	var visit func(file string, depth int)
	visit = func(file string, depth int) {
		if seen[file] || depth > 6 || files >= 300 {
			return
		}
		seen[file] = true
		raw, err := readInRoot(root, file)
		if err != nil {
			if file == k.config {
				notes = append(notes, k.config+" could not be read")
			}
			return
		}
		files++
		var incs []string
		var found []ProxySite
		switch kind {
		case ProxyNginx:
			ds := parseNginx(string(raw))
			found = nginxSites(ds, file)
			incs = nginxIncludes(ds, k.confDir)
		case ProxyCaddy:
			top, braceless := parseCaddy(string(raw))
			found = caddySites(top, file)
			incs = caddyImports(top, path.Dir(file))
			if braceless && file == k.config {
				notes = append(notes, "the Caddyfile is a single site without braces; wrap it in a site block so No-dal can add sites next to it")
			}
		}
		name, managed := managedFile(kind, file)
		for i := range found {
			if managed && strings.HasPrefix(string(raw), proxyMarker) {
				found[i].Managed, found[i].Name = true, name
				readManagedExtras(kind, string(raw), &found[i])
			}
		}
		sites = append(sites, found...)
		for _, pat := range incs {
			if globMatch(pat, k.managedPath(kind, "x")) {
				included = true
			}
			for _, f := range expandInRoot(root, pat) {
				visit(f, depth+1)
			}
		}
	}
	visit(k.config, 0)
	// Managed files are listed even when nothing includes them yet.
	if names, err := listInRoot(root, k.managedDir); err == nil {
		sort.Strings(names)
		for _, n := range names {
			f := k.managedDir + "/" + n
			if _, ok := managedFile(kind, f); ok && !seen[f] {
				seen[f] = true
				visit(f, 1)
			}
		}
	}
	if !included {
		if kind == ProxyNginx {
			notes = append(notes, "nginx.conf does not include "+k.include+"; sites No-dal adds will not load until it does")
		} else if existsInRoot(root, k.config) {
			notes = append(notes, "the Caddyfile does not import "+k.include+" yet; No-dal adds that line when you add a site")
		}
	}
	if sites == nil {
		sites = []ProxySite{}
	}
	return sites, included, notes
}

// readManagedExtras recovers settings that the site list does not show.
func readManagedExtras(kind, raw string, s *ProxySite) {
	if kind != ProxyNginx {
		return
	}
	s.Websocket = strings.Contains(raw, "proxy_set_header Upgrade")
	for _, d := range parseNginx(raw) {
		for _, x := range d.block {
			if x.name == "ssl_certificate" && len(x.args) > 0 {
				s.CertPath = x.args[0]
			}
			if x.name == "ssl_certificate_key" && len(x.args) > 0 {
				s.KeyPath = x.args[0]
			}
		}
	}
}

// expandInRoot lists the files a glob pattern matches inside root.
func expandInRoot(root, pattern string) []string {
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{pattern}
	}
	dir := path.Dir(pattern)
	if strings.ContainsAny(dir, "*?[") {
		return nil
	}
	names, err := listInRoot(root, dir)
	if err != nil {
		return nil
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		f := dir + "/" + n
		if globMatch(pattern, f) {
			out = append(out, f)
		}
	}
	return out
}

func (e *Engine) proxyValidate(ctx context.Context, machineID, kind string, k proxyKind, bin string) (string, error) {
	var out []byte
	var err error
	if kind == ProxyCaddy {
		out, err = e.machineRun(ctx, machineID, bin, "validate", "--config", k.config, "--adapter", "caddyfile")
	} else {
		out, err = e.machineRun(ctx, machineID, bin, "-t", "-q")
	}
	return strings.TrimSpace(string(out)), err
}

// ensureCaddyImport adds the import line for managed sites to the Caddyfile.
func ensureCaddyImport(root string, k proxyKind) (undo func(), err error) {
	raw, rerr := readInRoot(root, k.config)
	if rerr != nil && existsInRoot(root, k.config) {
		return nil, fmt.Errorf("the Caddyfile could not be read: %w", rerr)
	}
	top, braceless := parseCaddy(string(raw))
	for _, pat := range caddyImports(top, k.confDir) {
		if globMatch(pat, k.managedPath(ProxyCaddy, "x")) {
			return func() {}, nil
		}
	}
	if braceless {
		return nil, fmt.Errorf("the Caddyfile is a single site without braces; wrap it in a site block first, then add sites here")
	}
	next := string(raw)
	if next != "" && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	next += "\n# Sites managed by No-dal\nimport " + k.include + "\n"
	if err := writeInRoot(root, k.config, []byte(next), 0o644); err != nil {
		return nil, fmt.Errorf("could not add the import line to the Caddyfile: %w", err)
	}
	return func() { _ = writeInRoot(root, k.config, raw, 0o644) }, nil
}

func (e *Engine) proxyApply(ctx context.Context, req ProxyRequest, k proxyKind, root, bin string) (ProxyResult, error) {
	if req.Site == nil {
		return ProxyResult{}, fmt.Errorf("site is missing")
	}
	site, err := CleanProxySite(req.Kind, *req.Site)
	if err != nil {
		return ProxyResult{}, err
	}
	if req.Kind == ProxyNginx && site.TLS == ProxyTLSCert {
		for _, p := range []string{site.CertPath, site.KeyPath} {
			if !existsInRoot(root, p) {
				return ProxyResult{}, fmt.Errorf("%s does not exist on this machine", p)
			}
		}
	}
	if req.Previous != "" && !proxyNameRe.MatchString(req.Previous) {
		return ProxyResult{}, fmt.Errorf("previous site name is invalid")
	}
	// Refuse to take a domain another site on this proxy already answers.
	sites, included, _ := readProxySites(req.Kind, k, root)
	for _, s := range sites {
		if s.Managed && (s.Name == site.Name || s.Name == req.Previous) {
			continue
		}
		for _, d := range s.Domains {
			for _, want := range site.Domains {
				if strings.EqualFold(d, want) || strings.EqualFold(strings.TrimPrefix(d, "http://"), want) {
					return ProxyResult{}, fmt.Errorf("%s is already served by a site in %s", want, s.File)
				}
			}
		}
	}
	if req.Kind == ProxyNginx && !included {
		return ProxyResult{}, fmt.Errorf("nginx.conf does not include %s; add `include %s;` inside its http block first", k.include, k.include)
	}
	undoInclude := func() {}
	if req.Kind == ProxyCaddy {
		if undoInclude, err = ensureCaddyImport(root, k); err != nil {
			return ProxyResult{}, err
		}
	}
	file := k.managedPath(req.Kind, site.Name)
	type saved struct {
		path string
		data []byte
		had  bool
	}
	var backups []saved
	for _, p := range []string{file, k.managedPath(req.Kind, req.Previous)} {
		if p == file || req.Previous != "" && req.Previous != site.Name {
			b, rerr := readInRoot(root, p)
			backups = append(backups, saved{path: p, data: b, had: rerr == nil})
		}
	}
	restore := func() {
		for _, b := range backups {
			if b.had {
				_ = writeInRoot(root, b.path, b.data, 0o644)
			} else {
				_ = removeInRoot(root, b.path)
			}
		}
		undoInclude()
	}
	if err := writeInRoot(root, file, []byte(RenderProxySite(req.Kind, site)), 0o644); err != nil {
		restore()
		return ProxyResult{}, fmt.Errorf("could not write %s: %w", file, err)
	}
	if req.Previous != "" && req.Previous != site.Name {
		_ = removeInRoot(root, k.managedPath(req.Kind, req.Previous))
	}
	return e.proxyCommit(ctx, req, k, bin, site, file, restore)
}

// proxyCommit validates and reloads after a change, putting things back if
// either fails.
func (e *Engine) proxyCommit(ctx context.Context, req ProxyRequest, k proxyKind, bin string, site ProxySite, file string, restore func()) (ProxyResult, error) {
	if out, err := e.proxyValidate(ctx, req.MachineID, req.Kind, k, bin); err != nil {
		restore()
		return ProxyResult{Output: out}, fmt.Errorf("%s rejected the change, so nothing was changed: %s", req.Kind, lastLine([]byte(out), err))
	}
	state, _ := e.machineRun(ctx, req.MachineID, binSystemctl, "show", "-p", "ActiveState", "--value", k.service)
	if strings.TrimSpace(string(state)) != "active" {
		return ProxyResult{Site: &site, Message: "saved to " + file + "; " + k.service + " is not running, so it takes effect when it starts"}, nil
	}
	if out, err := e.machineRun(ctx, req.MachineID, binSystemctl, "reload", k.service); err != nil {
		restore()
		_, _ = e.machineRun(ctx, req.MachineID, binSystemctl, "reload", k.service)
		return ProxyResult{Output: string(out)}, fmt.Errorf("%s could not reload, so the change was put back: %s", k.service, lastLine(out, err))
	}
	return ProxyResult{Site: &site, Message: "saved to " + file + " and " + k.service + " reloaded"}, nil
}

func (e *Engine) proxyRemove(ctx context.Context, req ProxyRequest, k proxyKind, root, bin string) (ProxyResult, error) {
	if !proxyNameRe.MatchString(req.Name) {
		return ProxyResult{}, fmt.Errorf("site name is invalid")
	}
	file := k.managedPath(req.Kind, req.Name)
	raw, err := readInRoot(root, file)
	if err != nil {
		return ProxyResult{}, fmt.Errorf("No-dal has no site named %s on this machine", req.Name)
	}
	if !strings.HasPrefix(string(raw), proxyMarker) {
		return ProxyResult{}, fmt.Errorf("%s was not written by No-dal, so it is not removed", file)
	}
	if err := removeInRoot(root, file); err != nil {
		return ProxyResult{}, err
	}
	restore := func() { _ = writeInRoot(root, file, raw, 0o644) }
	res, err := e.proxyCommit(ctx, req, k, bin, ProxySite{Name: req.Name}, file, restore)
	if err == nil {
		res.Message = strings.Replace(res.Message, "saved to", "removed", 1)
	}
	return res, err
}

func (e *Engine) proxyInstall(ctx context.Context, req ProxyRequest, k proxyKind, root string) (ProxyResult, error) {
	if bin := findIn(root, k.bins); bin != "" {
		return ProxyResult{Message: req.Kind + " is already installed"}, nil
	}
	if !existsInRoot(root, binAptGet) {
		return ProxyResult{}, fmt.Errorf("this machine has no apt; install %s on it by hand", req.Kind)
	}
	if out, err := e.machineRun(ctx, req.MachineID, binAptGet, "update", "-q"); err != nil {
		return ProxyResult{Output: string(out)}, fmt.Errorf("apt-get update failed: %s", lastLine(out, err))
	}
	if out, err := e.machineRun(ctx, req.MachineID, "/usr/bin/env", "DEBIAN_FRONTEND=noninteractive", binAptGet, "install", "-y", "-q", k.service); err != nil {
		return ProxyResult{Output: string(out)}, fmt.Errorf("installing %s failed: %s", k.service, lastLine(out, err))
	}
	_, _ = e.machineRun(ctx, req.MachineID, binSystemctl, "enable", "--now", k.service)
	return ProxyResult{Message: k.service + " installed and started"}, nil
}
