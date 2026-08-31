package linux

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// OSRelease is the shared parse of /etc/os-release. Distro policy lives
// in hostos, not here.
type OSRelease struct {
	ID         string
	VersionID  string
	IDLike     []string
	Name       string
	PrettyName string
}

// ParseOSRelease reads KEY=value lines from an os-release document.
func ParseOSRelease(r io.Reader) (OSRelease, error) {
	var out OSRelease
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = unquote(strings.TrimSpace(value))
		switch key {
		case "ID":
			out.ID = strings.ToLower(value)
		case "VERSION_ID":
			out.VersionID = value
		case "ID_LIKE":
			out.IDLike = strings.Fields(strings.ToLower(value))
		case "NAME":
			out.Name = value
		case "PRETTY_NAME":
			out.PrettyName = value
		}
	}
	if err := sc.Err(); err != nil {
		return OSRelease{}, err
	}
	if out.ID == "" {
		return OSRelease{}, fmt.Errorf("os-release: missing ID")
	}
	return out, nil
}

func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
