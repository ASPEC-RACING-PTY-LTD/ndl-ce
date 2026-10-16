package docker

import (
	"path"
	"strings"
)

// Parsers for Nginx and Caddy configuration. They only look for sites: the
// names a site answers and where it sends requests. Other settings, and any
// secrets in the files, are skipped and never reported.

// confToken is one word or brace of a configuration file.
type confToken struct {
	text string
	line int
	// quoted tokens are never braces or semicolons.
	quoted bool
}

// tokenize splits Nginx or Caddy syntax into words, braces and (for Nginx)
// semicolons. Comments run from # to the end of the line.
func tokenize(src string, semicolons bool) []confToken {
	var out []confToken
	line := 1
	var cur strings.Builder
	curLine := 0
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, confToken{text: cur.String(), line: curLine})
			cur.Reset()
		}
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\n':
			flush()
			out = append(out, confToken{text: "\n", line: line})
			line++
		case c == ' ' || c == '\t' || c == '\r':
			flush()
		case c == '#' && cur.Len() == 0:
			for i < len(src) && src[i] != '\n' {
				i++
			}
			i--
		case c == '"' || c == '\'' || (c == '`' && !semicolons):
			flush()
			j := i + 1
			var b strings.Builder
			for j < len(src) && src[j] != c {
				if src[j] == '\\' && j+1 < len(src) {
					j++
				}
				if src[j] == '\n' {
					line++
				}
				b.WriteByte(src[j])
				j++
			}
			out = append(out, confToken{text: b.String(), line: line, quoted: true})
			i = j
		case c == '{' || c == '}' || (c == ';' && semicolons):
			// Caddy placeholders such as {host} stay part of their word.
			if !semicolons && c == '{' && i+1 < len(src) && src[i+1] != ' ' && src[i+1] != '\n' && src[i+1] != '\r' && src[i+1] != '\t' {
				end := strings.IndexByte(src[i:], '}')
				nl := strings.IndexByte(src[i:], '\n')
				if end > 0 && (nl < 0 || end < nl) {
					if cur.Len() == 0 {
						curLine = line
					}
					cur.WriteString(src[i : i+end+1])
					i += end
					continue
				}
			}
			flush()
			out = append(out, confToken{text: string(c), line: line})
		default:
			if cur.Len() == 0 {
				curLine = line
			}
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// --- Nginx ---

type ngxDirective struct {
	name  string
	args  []string
	block []ngxDirective
	line  int
}

func parseNginx(src string) []ngxDirective {
	toks := tokenize(src, true)
	var pos int
	var parse func(depth int) []ngxDirective
	parse = func(depth int) []ngxDirective {
		var out []ngxDirective
		var words []confToken
		for pos < len(toks) {
			t := toks[pos]
			pos++
			if t.text == "\n" && !t.quoted {
				continue
			}
			switch {
			case !t.quoted && t.text == ";":
				if len(words) > 0 {
					out = append(out, ngxDirective{name: words[0].text, args: tokText(words[1:]), line: words[0].line})
				}
				words = nil
			case !t.quoted && t.text == "{":
				d := ngxDirective{line: t.line}
				if len(words) > 0 {
					d.name, d.args, d.line = words[0].text, tokText(words[1:]), words[0].line
				}
				if depth < 32 {
					d.block = parse(depth + 1)
				}
				out = append(out, d)
				words = nil
			case !t.quoted && t.text == "}":
				return out
			default:
				words = append(words, t)
			}
		}
		return out
	}
	return parse(0)
}

func tokText(ts []confToken) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.text)
	}
	return out
}

// nginxSites lists the server blocks in parsed directives.
func nginxSites(ds []ngxDirective, file string) []ProxySite {
	var out []ProxySite
	for _, d := range ds {
		switch d.name {
		case "server":
			out = append(out, nginxSite(d, file))
		case "http":
			out = append(out, nginxSites(d.block, file)...)
		}
	}
	return out
}

func nginxSite(d ngxDirective, file string) ProxySite {
	s := ProxySite{File: file, Line: d.line}
	ssl := false
	for _, x := range d.block {
		switch x.name {
		case "server_name":
			for _, n := range x.args {
				if n != "_" && n != "" {
					s.Domains = append(s.Domains, n)
				}
			}
		case "listen":
			if len(x.args) > 0 {
				s.Listen = append(s.Listen, x.args[0])
			}
			for _, a := range x.args[1:] {
				if a == "ssl" || a == "quic" {
					ssl = true
				}
			}
		case "ssl_certificate":
			ssl = true
		case "return":
			if s.Upstream == "" && len(x.args) == 2 {
				s.Redirect = x.args[1]
			}
		case "location":
			if up := nginxProxyPass(x.block); up != "" && (s.Upstream == "" || strings.Join(x.args, " ") == "/") {
				s.Upstream = up
			}
		case "proxy_pass":
			if len(x.args) > 0 && s.Upstream == "" {
				s.Upstream = x.args[0]
			}
		}
	}
	if ssl {
		s.TLS = ProxyTLSCert
	} else {
		s.TLS = ProxyTLSHTTP
	}
	if len(s.Domains) == 0 && len(s.Listen) > 0 {
		s.Domains = []string{"default (" + strings.Join(s.Listen, ", ") + ")"}
	}
	return s
}

func nginxProxyPass(ds []ngxDirective) string {
	for _, d := range ds {
		if d.name == "proxy_pass" && len(d.args) > 0 {
			return d.args[0]
		}
		if d.name == "location" {
			if up := nginxProxyPass(d.block); up != "" {
				return up
			}
		}
	}
	return ""
}

// nginxIncludes lists the include patterns in parsed directives, relative
// patterns made absolute against the configuration directory.
func nginxIncludes(ds []ngxDirective, confDir string) []string {
	var out []string
	for _, d := range ds {
		if d.name == "include" && len(d.args) > 0 {
			p := d.args[0]
			if !strings.HasPrefix(p, "/") {
				p = path.Join(confDir, p)
			}
			out = append(out, p)
		}
		if len(d.block) > 0 && (d.name == "http" || d.name == "") {
			out = append(out, nginxIncludes(d.block, confDir)...)
		}
	}
	return out
}

// --- Caddy ---

type caddyBlock struct {
	// head is the words before the block's brace, line by line flattened.
	head  []string
	line  int
	lines [][]string
	inner []caddyBlock
}

// parseCaddy returns the top-level entries of a Caddyfile. Each line of
// words at the top level that is not followed by a block is returned as a
// block with no lines, which is how import lines and a braceless site's
// address show up.
func parseCaddy(src string) (top []caddyBlock, braceless bool) {
	toks := tokenize(src, false)
	pos := 0
	var parse func(depth int) caddyBlock
	parse = func(depth int) caddyBlock {
		var b caddyBlock
		var words []string
		wordLine := 0
		for pos < len(toks) {
			t := toks[pos]
			pos++
			switch {
			case !t.quoted && t.text == "\n":
				if len(words) > 0 {
					b.lines = append(b.lines, words)
					words = nil
				}
			case !t.quoted && t.text == "{":
				child := caddyBlock{head: words, line: wordLine}
				words = nil
				if depth < 32 {
					inner := parse(depth + 1)
					child.lines, child.inner = inner.lines, inner.inner
				}
				b.inner = append(b.inner, child)
			case !t.quoted && t.text == "}":
				if len(words) > 0 {
					b.lines = append(b.lines, words)
				}
				return b
			default:
				if len(words) == 0 {
					wordLine = t.line
				}
				words = append(words, t.text)
			}
		}
		if len(words) > 0 {
			b.lines = append(b.lines, words)
		}
		return b
	}
	root := parse(0)
	top = root.inner
	// A file with lines but no site block is a single braceless site.
	for _, l := range root.lines {
		if len(l) > 0 && l[0] != "import" {
			braceless = true
		}
	}
	for _, l := range root.lines {
		top = append(top, caddyBlock{head: l})
	}
	return top, braceless
}

// caddyImports lists the import patterns at the top level.
func caddyImports(top []caddyBlock, confDir string) []string {
	var out []string
	for _, b := range top {
		if len(b.head) >= 2 && b.head[0] == "import" && b.lines == nil && b.inner == nil {
			p := b.head[1]
			if !strings.HasPrefix(p, "/") {
				p = path.Join(confDir, p)
			}
			out = append(out, p)
		}
	}
	return out
}

// caddySites lists the site blocks at the top level of a Caddyfile.
func caddySites(top []caddyBlock, file string) []ProxySite {
	var out []ProxySite
	for _, b := range top {
		if len(b.head) == 0 {
			continue // global options
		}
		first := b.head[0]
		if strings.HasPrefix(first, "(") || first == "import" || (b.lines == nil && b.inner == nil) {
			continue // snippet, import line or a bare line
		}
		s := ProxySite{File: file, Line: b.line, TLS: ProxyTLSAuto}
		for _, a := range b.head {
			for _, part := range strings.Split(a, ",") {
				if part = strings.TrimSpace(part); part != "" {
					if strings.HasPrefix(part, "http://") {
						s.TLS = ProxyTLSHTTP
					}
					s.Domains = append(s.Domains, part)
				}
			}
		}
		walkCaddy(b, &s)
		out = append(out, s)
	}
	return out
}

func walkCaddy(b caddyBlock, s *ProxySite) {
	for _, l := range b.lines {
		caddyLine(l, s)
	}
	for _, in := range b.inner {
		caddyLine(in.head, s)
		walkCaddy(in, s)
	}
}

func caddyLine(l []string, s *ProxySite) {
	if len(l) == 0 {
		return
	}
	switch l[0] {
	case "reverse_proxy":
		// A proxy with no matcher serves the whole site and wins over one
		// that only serves a path.
		matched := false
		for _, a := range l[1:] {
			if strings.HasPrefix(a, "/") || strings.HasPrefix(a, "@") || a == "*" {
				matched = matched || a != "*"
				continue
			}
			if s.Upstream == "" || (s.upstreamMatched && !matched) {
				s.Upstream, s.upstreamMatched = a, matched
			}
			return
		}
	case "tls":
		if len(l) > 1 && l[1] == "internal" {
			s.TLS = ProxyTLSInternal
		}
	case "redir":
		if s.Upstream == "" && len(l) > 1 {
			s.Redirect = l[len(l)-1]
			if len(l) > 2 && !strings.HasPrefix(l[len(l)-1], "/") && !strings.Contains(l[len(l)-1], "://") {
				s.Redirect = l[len(l)-2]
			}
		}
	}
}

// globMatch reports whether name matches an include pattern.
func globMatch(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}
