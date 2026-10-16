package backupscope

import (
	"bytes"
	"context"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/backup"
)

// Smart leaves out anything that can be fetched or rebuilt again. These
// directory names are rebuildable wherever they appear (outside the
// operating system trees the prefix tables already cover).
var reproducibleNames = map[string]string{
	"node_modules":      "Package dependencies",
	".pnpm-store":       "Package cache",
	"__pycache__":       "Build cache",
	".cache":            "Cache",
	"cache":             "Cache",
	"caches":            "Cache",
	"Cache":             "Cache",
	"CachedData":        "Cache",
	"transcode":         "Transcode cache",
	"transcodes":        "Transcode cache",
	"transcoding-temp":  "Transcode cache",
	".npm":              "Package cache",
	".yarn-cache":       "Package cache",
	".rustup":           "Toolchain",
	".nvm":              "Toolchain",
	".bun":              "Toolchain",
	".vscode-server":    "Editor server",
	".cursor-server":    "Editor server",
	".gradle":           "Build cache",
	".pytest_cache":     "Build cache",
	".mypy_cache":       "Build cache",
	".ruff_cache":       "Build cache",
	".turbo":            "Build cache",
	".next":             "Build output",
	"steamapps":         "Game files",
	"go-build":          "Build cache",
	".terraform":        "Provider cache",
	"site-packages":     "Package dependencies",
	".venv":             "Package dependencies",
	"venv":              "Package dependencies",
	"vendor-cache":      "Package cache",
	"docker-buildcache": "Build cache",
}

// reproducibleSuffixes are rebuildable trees recognised by their last path
// segments.
var reproducibleSuffixes = []struct{ suffix, label string }{
	{".cargo/registry", "Package cache"},
	{".cargo/git", "Package cache"},
	{"go/pkg/mod", "Package cache"},
	{".m2/repository", "Package cache"},
	{".nuget/packages", "Package cache"},
	{".yarn/cache", "Package cache"},
	{".yarn/berry/cache", "Package cache"},
	{".local/share/pnpm", "Package cache"},
	{".local/share/containers", "Container images"},
	{"Content/Paks", "Game files"},
	{"Binaries/Win64", "Game files"},
	{"Binaries/Linux", "Game files"},
	{"Engine/Binaries", "Game files"},
	{"Engine/Content", "Game files"},
}

// reproducibleDir reports whether the directory rel is rebuildable, with an
// item ID prefix and label.
func reproducibleDir(rel string) (id, label string) {
	if strings.HasPrefix(rel, "etc/") || rel == "etc" {
		return "", ""
	}
	base := path.Base(rel)
	if l, ok := reproducibleNames[base]; ok {
		return "repro:" + base, l
	}
	for _, r := range reproducibleSuffixes {
		if rel == r.suffix || strings.HasSuffix(rel, "/"+r.suffix) {
			return "repro:" + r.suffix, r.label
		}
	}
	return "", ""
}

// skipReproducibleWalk reports whether a reproducible prefix tree is big and
// uninteresting enough not to be walked at all (its size is not counted).
func skipReproducibleWalk(id string) bool {
	switch id {
	case "repro:docker-overlay", "repro:docker-image", "repro:docker-buildkit", "repro:containerd",
		"repro:docker-containerd", "repro:docker-containers", "repro:docker-tmp", "repro:apt", "repro:cache":
		return true
	}
	return false
}

var mediaExt = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".wmv": true, ".flv": true,
	".webm": true, ".ts": true, ".m2ts": true, ".mpg": true, ".mpeg": true, ".vob": true,
	".mp3": true, ".flac": true, ".wav": true, ".aac": true, ".ogg": true, ".opus": true, ".m4a": true,
	".wma": true, ".aiff": true, ".aif": true, ".ape": true, ".dsf": true, ".alac": true,
}

const (
	// A folder is media when at least this much of it is video or audio
	// and that makes up mediaDirShare of its bytes.
	mediaDirMinBytes = 50 << 20
	mediaDirShare    = 0.8
	// A single video or audio file this large is media on its own.
	mediaFileMinBytes = 200 << 20
	mediaFileLimit    = 5000
)

type mediaTally struct {
	media map[string]int64
	total map[string]int64
	files []string
	// owner is, per folder, the bytes each preview item counted.
	owner map[string]map[string]int64
	sizes map[string]int64
}

func newMediaTally() *mediaTally {
	return &mediaTally{media: map[string]int64{}, total: map[string]int64{}, owner: map[string]map[string]int64{}, sizes: map[string]int64{}}
}

// ownedUnder is the bytes each item counted inside p (a folder or a file).
func (m *mediaTally) ownedUnder(p string) map[string]int64 {
	out := map[string]int64{}
	if size, ok := m.sizes[p]; ok {
		for owner := range m.owner[path.Dir(p)] {
			out[owner] += size
			break
		}
		return out
	}
	for dir, owners := range m.owner {
		if dir == p || strings.HasPrefix(dir, p+"/") {
			for o, b := range owners {
				out[o] += b
			}
		}
	}
	return out
}

func (m *mediaTally) add(rel string, size int64, owner string) {
	dir := path.Dir(rel)
	if m.owner[dir] == nil {
		m.owner[dir] = map[string]int64{}
	}
	m.owner[dir][owner] += size
	m.total[dir] += size
	if !mediaExt[strings.ToLower(path.Ext(rel))] {
		return
	}
	m.media[dir] += size
	if size >= mediaFileMinBytes && len(m.files) < mediaFileLimit {
		m.files = append(m.files, rel)
		m.sizes[rel] = size
	}
}

// excluded is the media folders and large media files. A folder counts
// when it is mostly video or audio itself, or when every folder with files
// in its subtree is (one item per folder with a small metadata file next to
// it) and it is at least two levels deep. The outermost such folder is
// chosen, so a folder holding anything else, such as a config file, is
// never swallowed by a media sibling.
func (m *mediaTally) excluded() []string {
	subMedia := map[string]int64{}
	mixed := map[string]bool{}
	for dir, b := range m.total {
		notMedia := float64(m.media[dir]) < mediaDirShare*float64(b)
		for d := dir; d != "." && d != "/" && d != ""; d = path.Dir(d) {
			subMedia[d] += m.media[dir]
			if notMedia {
				mixed[d] = true
			}
		}
	}
	chosen := map[string]bool{}
	for dir, b := range m.media {
		if b >= mediaDirMinBytes && float64(b) >= mediaDirShare*float64(m.total[dir]) {
			chosen[dir] = true
		}
	}
	for dir, b := range subMedia {
		if strings.Count(dir, "/") >= 1 && b >= mediaDirMinBytes && !mixed[dir] {
			chosen[dir] = true
		}
	}
	var out []string
	for dir := range chosen {
		outer := false
		for d := path.Dir(dir); d != "." && d != "/" && d != ""; d = path.Dir(d) {
			if chosen[d] {
				outer = true
				break
			}
		}
		if !outer {
			out = append(out, dir)
		}
	}
	for _, f := range m.files {
		covered := false
		for d := path.Dir(f); d != "." && d != "/" && d != ""; d = path.Dir(d) {
			if chosen[d] {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

var keepConfigExt = map[string]bool{
	".yml": true, ".yaml": true, ".json": true, ".toml": true, ".ini": true, ".conf": true,
	".cfg": true, ".config": true, ".properties": true, ".xml": true, ".env": true,
}

var keepKeyExt = map[string]bool{
	".pem": true, ".key": true, ".crt": true, ".p12": true, ".pfx": true, ".jks": true, ".keystore": true,
}

// keepBuildDirs are output folders whose JSON or YAML is generated, not
// configuration.
var keepBuildDirs = map[string]bool{
	"dist": true, "build": true, "out": true, "target": true, "coverage": true, "vendor": true,
	"logs": true, "log": true, "tmp": true, "public": true, "static": true, ".svelte-kit": true,
}

const keepConfigMaxBytes = 5 << 20

// keepInRepo reports whether a file inside a Git working tree is a
// candidate to keep when Git does not track it: .env files, databases, keys
// and configuration. Its contents are never read.
func keepInRepo(rel string, size int64) bool {
	name := path.Base(rel)
	lower := strings.ToLower(name)
	if lower == ".env" || strings.HasPrefix(lower, ".env.") || strings.HasSuffix(lower, ".env") {
		return true
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		lower = strings.TrimSuffix(lower, suffix)
	}
	ext := path.Ext(lower)
	if sqliteExt[ext] || ext == ".db3" {
		return true
	}
	if keepKeyExt[ext] {
		return size <= keepConfigMaxBytes
	}
	if !keepConfigExt[ext] || size > keepConfigMaxBytes {
		return false
	}
	for _, seg := range strings.Split(path.Dir(rel), "/") {
		if keepBuildDirs[seg] {
			return false
		}
	}
	return true
}

// bindHoldsGit reports whether a Docker bind source is a Git working tree
// or contains one.
func bindHoldsGit(bind string, gitRoots map[string]string) bool {
	b := strings.Trim(filepath.ToSlash(bind), "/")
	for rel := range gitRoots {
		if b == rel || b == "" || strings.HasPrefix(rel, b+"/") {
			return true
		}
	}
	return false
}

// runGit runs git read-only against a working tree that belongs to a
// workload. Repository configuration is untrusted: hooks, fsmonitor and
// every transport are disabled, global and system config are ignored, and
// nothing is fetched. Tests replace it.
var runGit = func(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	full := append([]string{
		"-c", "safe.directory=*", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
		"-c", "protocol.allow=never", "-c", "core.untrackedCache=false", "-c", "diff.external=",
		"-C", dir,
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME=/nonexistent",
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "PATH=/usr/bin:/bin",
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}

// inspectGit records a working tree and returns the set of tracked paths
// (relative to the tree). It reads Git metadata only, never file contents.
func inspectGit(dir, guestPath string) (backup.GitRepo, map[string]bool) {
	repo := backup.GitRepo{Path: guestPath}
	tracked := map[string]bool{}
	out, err := runGit(dir, "ls-files", "-z")
	if err != nil {
		repo.Error = "could not read the repository"
		return repo, tracked
	}
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			tracked[string(f)] = true
		}
	}
	if out, err := runGit(dir, "rev-parse", "HEAD"); err == nil {
		repo.Commit = strings.TrimSpace(string(out))
	}
	if out, err := runGit(dir, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		repo.Branch = strings.TrimSpace(string(out))
	}
	if out, err := runGit(dir, "remote", "-v"); err == nil {
		seen := map[string]bool{}
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || seen[f[0]] {
				continue
			}
			seen[f[0]] = true
			repo.Remotes = append(repo.Remotes, backup.GitRemote{Name: f[0], URL: SafeRemoteURL(f[1])})
		}
	}
	if out, err := runGit(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all"); err == nil {
		for _, e := range bytes.Split(out, []byte{0}) {
			if len(e) < 4 {
				continue
			}
			repo.Dirty = true
			if len(repo.Changed) < backup.GitChangedLimit {
				repo.Changed = append(repo.Changed, string(e[3:]))
			}
		}
	}
	if repo.Commit != "" && len(repo.Remotes) > 0 {
		out, err := runGit(dir, "branch", "-r", "--contains", repo.Commit)
		repo.Unpushed = err != nil || strings.TrimSpace(string(out)) == ""
		if repo.Unpushed {
			// The commits only this tree has, newest first; the parent of the
			// oldest is the newest one a clone can get.
			if out, err := runGit(dir, "rev-list", "--first-parent", repo.Commit, "--not", "--remotes"); err == nil {
				ids := strings.Fields(string(out))
				repo.UnpushedCommits = len(ids)
				if len(ids) > 0 {
					if base, err := runGit(dir, "rev-parse", "--verify", "-q", ids[len(ids)-1]+"^"); err == nil {
						repo.PushedBase = strings.TrimSpace(string(base))
					}
				}
			}
		}
	} else if repo.Commit != "" {
		repo.Unpushed = true
	}
	return repo, tracked
}

// SafeRemoteURL drops any credentials from a remote URL.
func SafeRemoteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		u.User = nil
		return u.String()
	}
	return raw
}

// secretNames are files that hold credentials whatever their extension.
var secretNames = map[string]bool{
	".netrc": true, ".npmrc": true, ".pypirc": true, ".pgpass": true, ".git-credentials": true,
	".htpasswd": true, "id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
	"credentials": true, "known_hosts": true, "authorized_keys": true,
}

var secretExt = map[string]bool{".tfstate": true, ".tfvars": true, ".kdbx": true, ".keystore": true, ".jks": true}

// SecretLike reports whether a path looks like it holds secrets or local
// state that must never be committed on the owner's behalf: .env files,
// keys and certificates, databases, credential files, or names that say
// secret, token, password or credential. Only the name is looked at.
func SecretLike(rel string) bool {
	name := strings.ToLower(path.Base(filepath.ToSlash(rel)))
	if name == ".env" || strings.HasPrefix(name, ".env.") || strings.HasSuffix(name, ".env") || secretNames[name] {
		return true
	}
	for _, w := range []string{"secret", "token", "passw", "credential", "private"} {
		if strings.Contains(name, w) {
			return true
		}
	}
	base := name
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		base = strings.TrimSuffix(base, suffix)
	}
	ext := path.Ext(base)
	return keepKeyExt[ext] || sqliteExt[ext] || ext == ".db3" || secretExt[ext]
}

// UntrackedFiles lists files in the repository at root/guestRepo that Git
// neither tracks nor ignores, relative to the repository. Read only.
func UntrackedFiles(root, guestRepo string) ([]string, error) {
	out, err := runGit(filepath.Join(root, strings.TrimPrefix(guestRepo, "/")), "ls-files", "-o", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	return files, nil
}
