package backup

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

const maxInspectItems = 200

// InspectGuest fills Blueprint OS, packages, and services from a live root
// without reading secret file contents.
func InspectGuest(root string, bp *Blueprint) {
	if bp == nil || strings.TrimSpace(root) == "" {
		return
	}
	if bp.OS == "" {
		bp.OS = readOSRelease(root)
	}
	if len(bp.Packages) == 0 {
		bp.Packages = readPackages(root)
	}
	if len(bp.Services) == 0 {
		bp.Services = readServices(root)
	}
}

func readOSRelease(root string) string {
	for _, rel := range []string{"etc/os-release", "usr/lib/os-release"} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		id, pretty := "", ""
		for _, line := range strings.Split(string(raw), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			v = strings.Trim(v, `"'`)
			switch k {
			case "PRETTY_NAME":
				pretty = v
			case "ID":
				id = v
			}
		}
		return firstNonEmpty(pretty, id)
	}
	return ""
}

func readPackages(root string) []string {
	path := filepath.Join(root, "var/lib/dpkg/status")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "Package: ") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "Package: "))
		if name == "" {
			continue
		}
		out = append(out, name)
		if len(out) >= maxInspectItems {
			break
		}
	}
	return out
}

func readServices(root string) []string {
	dir := filepath.Join(root, "etc/systemd/system")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".service") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".service"))
		if len(out) >= maxInspectItems {
			break
		}
	}
	return out
}
