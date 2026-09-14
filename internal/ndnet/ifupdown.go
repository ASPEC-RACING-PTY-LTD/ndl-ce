package ndnet

import (
	"strings"
)

const ifupdownMigratePrefix = "# ndl-migrated: "

// disableIfupdownIface comments out auto/allow-hotplug/iface stanzas for iface.
// Other interfaces and comments are left unchanged.
func disableIfupdownIface(content, iface string) (string, bool) {
	iface = strings.TrimSpace(iface)
	if iface == "" || !ValidIfName(iface) {
		return content, false
	}
	lines := strings.Split(content, "\n")
	changed := false
	inStanza := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			inStanza = false
			continue
		}
		raw := trim
		if strings.HasPrefix(raw, ifupdownMigratePrefix) {
			continue
		}
		if strings.HasPrefix(raw, "#") {
			continue
		}
		if isIfupdownStanzaStart(raw) {
			inStanza = ifupdownStanzaMatches(raw, iface)
		} else if ifupdownControlLine(raw) {
			inStanza = false
			if !ifupdownControlMatches(raw, iface) {
				continue
			}
		} else if !inStanza {
			continue
		}
		if inStanza || ifupdownControlMatches(raw, iface) {
			lines[i] = ifupdownMigratePrefix + line
			changed = true
		}
	}
	if !changed {
		return content, false
	}
	return strings.Join(lines, "\n"), true
}

func ifupdownMentions(content, iface string) bool {
	iface = strings.TrimSpace(iface)
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		if ifupdownControlMatches(trim, iface) || ifupdownStanzaMatches(trim, iface) {
			return true
		}
	}
	return false
}

func isIfupdownStanzaStart(trim string) bool {
	fields := strings.Fields(trim)
	return len(fields) >= 2 && fields[0] == "iface"
}

func ifupdownControlLine(trim string) bool {
	fields := strings.Fields(trim)
	if len(fields) < 2 {
		return false
	}
	switch fields[0] {
	case "auto", "allow-hotplug", "allow-auto":
		return true
	}
	return false
}

func ifupdownStanzaMatches(trim, iface string) bool {
	fields := strings.Fields(trim)
	return len(fields) >= 2 && fields[0] == "iface" && sameIface(fields[1], iface)
}

func ifupdownControlMatches(trim, iface string) bool {
	fields := strings.Fields(trim)
	if len(fields) < 2 || !ifupdownControlLine(trim) {
		return false
	}
	for _, name := range fields[1:] {
		if sameIface(name, iface) {
			return true
		}
	}
	return false
}
