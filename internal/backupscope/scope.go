// Package backupscope classifies a live workload filesystem into persistent
// application data versus reproducible or disposable content. It uses
// structural metadata (paths, Docker inventory, service conventions) and
// never reads secret file contents.
package backupscope

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/no-dal/ndl-ce/internal/backup"
)

const (
	ModeSmart  = backup.CaptureModeSmart
	ModeCustom = backup.CaptureModeCustom
	ModeFull   = backup.CaptureModeFull

	KindDatabase     = "database"
	KindDockerVolume = "docker-volume"
	KindDockerBind   = "docker-bind"
	KindApplication  = "application"
	KindConfig       = "configuration"
	KindCertificate  = "certificate"
	KindGit          = "git"
	KindReproducible = "reproducible"
	KindLogs         = "logs"
	KindOS           = "os"
	KindUncertain    = "uncertain"
	KindTechnical    = "technical"
)

// Item is one discovered path category for preview and capture planning.
type Item struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Label       string   `json:"label"`
	Paths       []string `json:"paths"`
	Bytes       int64    `json:"bytes"`
	Selected    bool     `json:"selected"`
	DefaultOn   bool     `json:"default_on"`
	Excluded    bool     `json:"excluded"`
	Reproducible bool    `json:"reproducible,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// Warning is a user-visible classification concern. It never includes secrets.
type Warning struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	ItemID  string `json:"item_id,omitempty"`
	Path    string `json:"path,omitempty"`
}

// Selection is the user's override for one workload.
type Selection struct {
	Selected   []string `json:"selected,omitempty"`
	Deselected []string `json:"deselected,omitempty"`
	Includes   []string `json:"includes,omitempty"`
	Excludes   []string `json:"excludes,omitempty"`
}

// Preview is the per-workload scope report used by the policy editor.
type Preview struct {
	WorkloadID    string    `json:"workload_id,omitempty"`
	WorkloadName  string    `json:"workload_name,omitempty"`
	Mode          string    `json:"mode"`
	Items         []Item    `json:"items"`
	Warnings      []Warning `json:"warnings,omitempty"`
	ProtectedBytes int64    `json:"protected_bytes"`
	ExcludedBytes  int64    `json:"excluded_bytes"`
	FullBytes      int64    `json:"full_bytes"`
	Includes       []string `json:"includes,omitempty"`
	Excludes       []string `json:"excludes,omitempty"`
}

// DockerHint is a secret-free persistence hint from Docker inventory.
type DockerHint struct {
	EngineVersion  string
	ComposeVersion string
	Projects       []string
	NamedVolumes   []string
	BindMounts     []string
	Images         []string
	LocalImages    []string
}

// Options control discovery.
type Options struct {
	Root       string
	WorkloadID string
	Name       string
	Mode       string
	Selection  Selection
	Docker     *DockerHint
}

var technicalRoots = []string{"proc", "sys", "dev", "run"}

var persistentPrefixes = []struct {
	rel   string
	id    string
	kind  string
	label string
}{
	{"var/lib/postgresql", "db:postgresql", KindDatabase, "PostgreSQL"},
	{"var/lib/pgsql", "db:pgsql", KindDatabase, "PostgreSQL"},
	{"var/lib/mysql", "db:mysql", KindDatabase, "MySQL"},
	{"var/lib/mariadb", "db:mariadb", KindDatabase, "MariaDB"},
	{"var/lib/redis", "db:redis", KindDatabase, "Redis"},
	{"var/lib/mongodb", "db:mongodb", KindDatabase, "MongoDB"},
	{"var/lib/docker/volumes", "docker:volumes", KindDockerVolume, "Docker volumes"},
	{"etc/ssl", "certs:ssl", KindCertificate, "TLS material"},
	{"etc/letsencrypt", "certs:letsencrypt", KindCertificate, "Let's Encrypt"},
	{"etc/systemd/system", "config:systemd", KindConfig, "systemd units"},
	{"etc", "config:etc", KindConfig, "Configuration"},
	{"srv", "app:srv", KindApplication, "Application data"},
	{"opt", "app:opt", KindApplication, "Application data"},
	{"home", "app:home", KindApplication, "User data"},
	{"root", "app:root", KindApplication, "Root home"},
	{"var/lib", "app:var-lib", KindApplication, "Service state"},
}

var reproduciblePrefixes = []struct {
	rel   string
	id    string
	kind  string
	label string
}{
	{"var/cache/apt", "repro:apt", KindReproducible, "Package cache"},
	{"var/cache/apk", "repro:apk", KindReproducible, "Package cache"},
	{"var/cache", "repro:cache", KindReproducible, "Package cache"},
	{"var/lib/apt", "repro:apt-lib", KindReproducible, "Package cache"},
	{"var/tmp", "repro:var-tmp", KindReproducible, "Temporary files"},
	{"tmp", "repro:tmp", KindReproducible, "Temporary files"},
	{"var/log", "repro:logs", KindLogs, "Logs"},
	{"var/lib/docker/overlay2", "repro:docker-overlay", KindReproducible, "Docker image layers"},
	{"var/lib/docker/image", "repro:docker-image", KindReproducible, "Docker image metadata"},
	{"var/lib/docker/buildkit", "repro:docker-buildkit", KindReproducible, "Docker build cache"},
	{"usr", "os:usr", KindOS, "Operating system"},
	{"lib", "os:lib", KindOS, "Operating system"},
	{"lib64", "os:lib64", KindOS, "Operating system"},
	{"bin", "os:bin", KindOS, "Operating system"},
	{"sbin", "os:sbin", KindOS, "Operating system"},
	{"boot", "os:boot", KindOS, "Operating system"},
}

var sqliteExt = map[string]bool{".sqlite": true, ".sqlite3": true, ".db": true}

// Discover classifies root without reading secret contents. Sizes come from
// directory metadata (file size), not file body reads.
func Discover(opts Options) (Preview, error) {
	root := filepath.Clean(opts.Root)
	mode := strings.TrimSpace(opts.Mode)
	if mode == "" {
		mode = ModeSmart
	}
	items := map[string]*Item{}
	upsert := func(id, kind, label string, defaultOn bool, repro bool) *Item {
		if it, ok := items[id]; ok {
			return it
		}
		it := &Item{ID: id, Kind: kind, Label: label, DefaultOn: defaultOn, Reproducible: repro}
		items[id] = it
		return it
	}

	var warnings []Warning
	var full int64
	gitRoots := map[string]string{}
	sqliteHits := map[string]int64{}

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() && isTechnical(rel) {
			it := upsert("tech:"+firstSeg(rel), KindTechnical, "Runtime filesystem", false, true)
			it.Excluded = true
			addPath(it, "/"+rel)
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size := int64(0)
		if info.Mode().IsRegular() {
			size = info.Size()
			full += size
		}

		if d.IsDir() && looksLikeGit(path) {
			id := "git:" + rel
			it := upsert(id, KindGit, "Git repository", false, true)
			it.Reproducible = true
			addPath(it, "/"+rel)
			gitRoots[rel] = id
			it.Warnings = appendUnique(it.Warnings, "Git repository may contain uncommitted work, ignored files, or local-only state.")
			warnings = append(warnings, Warning{Level: "warning", Message: "Git repository may contain uncommitted work or local-only files.", ItemID: id, Path: "/" + rel})
		}

		if info.Mode().IsRegular() && sqliteExt[strings.ToLower(filepath.Ext(rel))] && !underReproCache(rel) {
			sqliteHits[rel] = size
		}

		if d.IsDir() && (filepath.Base(rel) == "node_modules" || filepath.Base(rel) == ".pnpm-store" || filepath.Base(rel) == "__pycache__") {
			id := "repro:" + filepath.Base(rel) + ":" + rel
			it := upsert(id, KindReproducible, filepath.Base(rel), false, true)
			addPath(it, "/"+rel)
			return nil
		}

		if classified := classifyPrefix(rel, persistentPrefixes); classified != nil && !isTechnical(rel) {
			defaultOn := classified.kind != KindOS
			if classified.id == "config:etc" && (strings.HasPrefix(rel, "etc/ssl") || strings.HasPrefix(rel, "etc/letsencrypt") || strings.HasPrefix(rel, "etc/systemd")) {
				// more specific cert/systemd items own those trees
			} else if classified.id == "app:var-lib" && moreSpecificPersistent(rel) {
				// database / docker volume items own those trees
			} else if classified.id == "app:opt" || classified.id == "app:root" || classified.id == "app:home" || classified.id == "app:srv" || classified.id == "app:var-lib" {
				if d.IsDir() && strings.Count(rel, "/") >= 1 {
					id := "app:" + rel
					it := upsert(id, KindApplication, "Application data", true, false)
					addPath(it, "/"+rel)
					it.Bytes += size
				} else {
					it := upsert(classified.id, classified.kind, classified.label, defaultOn, false)
					addPath(it, "/"+classified.rel)
					it.Bytes += size
				}
			} else {
				it := upsert(classified.id, classified.kind, classified.label, defaultOn, false)
				addPath(it, "/"+classified.rel)
				it.Bytes += size
			}
			return nil
		}
		if classified := classifyPrefix(rel, reproduciblePrefixes); classified != nil {
			it := upsert(classified.id, classified.kind, classified.label, false, true)
			addPath(it, "/"+classified.rel)
			it.Bytes += size
			if d.IsDir() && (classified.rel == rel || classified.rel == firstSeg(rel)) &&
				(classified.id == "repro:docker-overlay" || classified.id == "os:usr" || classified.id == "repro:tmp" || classified.id == "repro:var-tmp") {
				if d.IsDir() && rel == classified.rel {
					return nil
				}
			}
			return nil
		}
		if !d.IsDir() && size > 0 && !isTechnical(rel) {
			it := upsert("uncertain:"+filepath.Dir(rel), KindUncertain, "Unclassified data", true, false)
			addPath(it, "/"+filepath.ToSlash(filepath.Dir(rel)))
			it.Bytes += size
			if len(it.Warnings) == 0 {
				it.Warnings = append(it.Warnings, "Directory classification uncertain.")
				warnings = append(warnings, Warning{Level: "info", Message: "Directory classification uncertain.", ItemID: it.ID, Path: "/" + filepath.ToSlash(filepath.Dir(rel))})
			}
		}
		return nil
	})
	if walkErr != nil {
		return Preview{}, walkErr
	}

	for rel, size := range sqliteHits {
		id := "db:sqlite:" + rel
		it := upsert(id, KindDatabase, "SQLite database", true, false)
		addPath(it, "/"+rel)
		it.Bytes += size
		if gitID := enclosingGit(rel, gitRoots); gitID != "" {
			msg := "SQLite database detected inside a Git working tree."
			it.Warnings = appendUnique(it.Warnings, msg)
			warnings = append(warnings, Warning{Level: "warning", Message: msg, ItemID: id, Path: "/" + rel})
			if git := items[gitID]; git != nil {
				git.Warnings = appendUnique(git.Warnings, "SQLite database detected inside excluded path.")
			}
		}
	}

	if opts.Docker != nil {
		for _, vol := range opts.Docker.NamedVolumes {
			vol = strings.TrimSpace(vol)
			if vol == "" {
				continue
			}
			id := "docker:volume:" + vol
			it := upsert(id, KindDockerVolume, "Docker volume", true, false)
			addPath(it, vol)
		}
		for _, bind := range opts.Docker.BindMounts {
			bind = strings.TrimSpace(bind)
			if bind == "" {
				continue
			}
			id := "docker:bind:" + bind
			it := upsert(id, KindDockerBind, "Docker bind mount", true, false)
			addPath(it, bind)
			it.Warnings = appendUnique(it.Warnings, "Docker bind mount contains persistent application data.")
			warnings = append(warnings, Warning{Level: "info", Message: "Docker bind mount contains persistent application data.", ItemID: id, Path: bind})
		}
		if len(opts.Docker.Projects) > 0 {
			it := upsert("docker:compose", KindConfig, "Compose configuration", true, false)
			for _, p := range opts.Docker.Projects {
				addPath(it, p)
			}
		}
	}

	list := make([]Item, 0, len(items))
	for _, it := range items {
		list = append(list, *it)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Kind == list[j].Kind {
			return list[i].ID < list[j].ID
		}
		return list[i].Kind < list[j].Kind
	})

	prev := Preview{
		WorkloadID: opts.WorkloadID, WorkloadName: opts.Name, Mode: mode,
		Items: list, Warnings: warnings, FullBytes: full,
	}
	applySelection(&prev, opts.Selection)
	return prev, nil
}

// PlanCapture turns a preview into include/exclude path lists for the engine.
func PlanCapture(prev Preview) (includes, excludes []string) {
	if prev.Mode == ModeFull {
		for _, it := range prev.Items {
			if it.Kind == KindTechnical {
				excludes = append(excludes, it.Paths...)
			}
		}
		return nil, uniquePaths(excludes)
	}
	for _, it := range prev.Items {
		if it.Kind == KindTechnical {
			excludes = append(excludes, it.Paths...)
			continue
		}
		if it.Selected {
			includes = append(includes, it.Paths...)
			continue
		}
		excludes = append(excludes, it.Paths...)
	}
	includes = uniquePaths(includes)
	excludes = uniquePaths(excludes)
	return includes, excludes
}

func applySelection(prev *Preview, sel Selection) {
	forced := map[string]bool{}
	for _, id := range sel.Selected {
		forced[id] = true
	}
	denied := map[string]bool{}
	for _, id := range sel.Deselected {
		denied[id] = true
	}
	var protected, excluded int64
	for i := range prev.Items {
		it := &prev.Items[i]
		on := it.DefaultOn && !it.Excluded
		if prev.Mode == ModeFull && it.Kind != KindTechnical {
			on = true
		}
		if prev.Mode == ModeCustom && len(sel.Selected)+len(sel.Deselected)+len(sel.Includes)+len(sel.Excludes) == 0 {
			on = it.DefaultOn && !it.Reproducible && it.Kind != KindTechnical
		}
		if forced[it.ID] {
			on = true
		}
		if denied[it.ID] {
			on = false
		}
		it.Selected = on
		if on {
			protected += it.Bytes
		} else {
			excluded += it.Bytes
		}
	}
	if extra := uniquePaths(sel.Includes); len(extra) > 0 {
		prev.Items = append(prev.Items, Item{
			ID: "custom:includes", Kind: KindApplication, Label: "Custom includes",
			Paths: extra, Selected: true, DefaultOn: true,
		})
	}
	if extra := uniquePaths(sel.Excludes); len(extra) > 0 {
		prev.Items = append(prev.Items, Item{
			ID: "custom:excludes", Kind: KindReproducible, Label: "Custom excludes",
			Paths: extra, Selected: false, DefaultOn: false, Excluded: true, Reproducible: true,
		})
		for _, p := range extra {
			for i := range prev.Items {
				if prev.Items[i].Selected && pathOverlap(p, prev.Items[i].Paths) && isDatabaseKind(prev.Items[i].Kind) {
					msg := "Custom exclusion overlaps detected database storage."
					prev.Items[i].Warnings = appendUnique(prev.Items[i].Warnings, msg)
					prev.Warnings = append(prev.Warnings, Warning{Level: "warning", Message: msg, ItemID: prev.Items[i].ID, Path: p})
				}
			}
		}
	}
	incs, excs := PlanCapture(*prev)
	prev.Includes = incs
	prev.Excludes = excs
	prev.ProtectedBytes = protected
	prev.ExcludedBytes = excluded
}

func isDatabaseKind(kind string) bool {
	return kind == KindDatabase
}

func pathOverlap(p string, paths []string) bool {
	p = strings.TrimRight(filepath.ToSlash(p), "/")
	for _, other := range paths {
		o := strings.TrimRight(filepath.ToSlash(other), "/")
		if p == o || strings.HasPrefix(p, o+"/") || strings.HasPrefix(o, p+"/") {
			return true
		}
	}
	return false
}

func classifyPrefix(rel string, table []struct {
	rel   string
	id    string
	kind  string
	label string
}) *struct {
	rel   string
	id    string
	kind  string
	label string
} {
	var best *struct {
		rel   string
		id    string
		kind  string
		label string
	}
	bestLen := -1
	for i := range table {
		pref := table[i].rel
		if rel == pref || strings.HasPrefix(rel, pref+"/") {
			if len(pref) > bestLen {
				best = &table[i]
				bestLen = len(pref)
			}
		}
	}
	return best
}

func moreSpecificPersistent(rel string) bool {
	for _, p := range []string{"var/lib/postgresql", "var/lib/pgsql", "var/lib/mysql", "var/lib/mariadb", "var/lib/redis", "var/lib/mongodb", "var/lib/docker"} {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

func isTechnical(rel string) bool {
	seg := firstSeg(rel)
	for _, t := range technicalRoots {
		if seg == t {
			return true
		}
	}
	return false
}

func underReproCache(rel string) bool {
	return strings.HasPrefix(rel, "var/cache/") || strings.HasPrefix(rel, "tmp/") || strings.HasPrefix(rel, "var/tmp/") || strings.Contains(rel, "/node_modules/")
}

func looksLikeGit(dir string) bool {
	st, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && (st.IsDir() || st.Mode().IsRegular())
}

func enclosingGit(rel string, roots map[string]string) string {
	for root, id := range roots {
		if rel == root || strings.HasPrefix(rel, root+"/") {
			return id
		}
	}
	return ""
}

func firstSeg(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

func addPath(it *Item, p string) {
	p = filepath.ToSlash(p)
	if p == "" {
		return
	}
	for _, e := range it.Paths {
		if e == p {
			return
		}
	}
	it.Paths = append(it.Paths, p)
}

func uniquePaths(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") && !strings.Contains(p, "/") {
			// named docker volume
		} else if !strings.HasPrefix(p, "/") {
			p = "/" + strings.TrimPrefix(p, "./")
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func appendUnique(in []string, msg string) []string {
	for _, e := range in {
		if e == msg {
			return in
		}
	}
	return append(in, msg)
}
