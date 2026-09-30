package gameserver

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	githubRepoPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	safeVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,79}$`)
	urlVarPattern      = regexp.MustCompile(`\{\{([A-Z][A-Z0-9_]*)\}\}`)
)

func (t Template) versionEnv() string {
	if t.Install != nil && t.Install.VersionEnv != "" {
		return t.Install.VersionEnv
	}
	return "VERSION"
}

func archiveKind(d Download, name string) string {
	if d.Archive != "" {
		return d.Archive
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"):
		return "tar.xz"
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"):
		return "tar.bz2"
	case strings.HasSuffix(lower, ".tar"):
		return "tar"
	default:
		return "file"
	}
}

func validateDownloadTemplate(t Template) error {
	if t.Install == nil || len(t.Install.Downloads) == 0 {
		return fmt.Errorf("%s template needs install.downloads", t.InstallBuiltin)
	}
	for i, d := range t.Install.Downloads {
		switch {
		case d.Repo != "":
			if !githubRepoPattern.MatchString(d.Repo) {
				return fmt.Errorf("download %d repo %q is not owner/name", i, d.Repo)
			}
			if d.Asset == "" {
				return fmt.Errorf("download %d needs an asset pattern", i)
			}
			if _, err := regexp.Compile(d.Asset); err != nil {
				return fmt.Errorf("download %d asset pattern: %v", i, err)
			}
			// A pinned-version URL lets a specific release install without
			// the GitHub API; it must point at this repository's releases.
			if d.URL != "" && !strings.HasPrefix(d.URL, "https://github.com/"+d.Repo+"/releases/download/") {
				return fmt.Errorf("download %d URL must be a release download of %s", i, d.Repo)
			}
			if d.URL != "" {
				for _, m := range urlVarPattern.FindAllStringSubmatch(d.URL, -1) {
					if _, ok := t.variable(m[1]); !ok {
						return fmt.Errorf("download %d URL uses undeclared variable %s", i, m[1])
					}
				}
			}
		case d.URL != "":
			u, err := url.Parse(urlVarPattern.ReplaceAllString(d.URL, "x"))
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return fmt.Errorf("download %d URL must be https", i)
			}
			for _, m := range urlVarPattern.FindAllStringSubmatch(d.URL, -1) {
				if _, ok := t.variable(m[1]); !ok {
					return fmt.Errorf("download %d URL uses undeclared variable %s", i, m[1])
				}
			}
		default:
			return fmt.Errorf("download %d needs repo or url", i)
		}
		if t.InstallBuiltin == "github-release" && d.Repo == "" {
			return fmt.Errorf("github-release download %d needs a repo", i)
		}
		switch d.Archive {
		case "", "zip", "tar", "tar.gz", "tar.xz", "tar.bz2", "file":
		default:
			return fmt.Errorf("download %d archive %q is not supported", i, d.Archive)
		}
		if d.Strip < 0 || d.Strip > 4 {
			return fmt.Errorf("download %d strip out of range", i)
		}
		for _, p := range append(append([]string{d.Dest, d.Subdir}, d.Executables...), d.Keep...) {
			if p == "" {
				continue
			}
			if _, err := cleanArchiveRel(p); err != nil || strings.HasPrefix(p, "/") {
				return fmt.Errorf("download %d path %q is unsafe", i, p)
			}
		}
		if d.SHA256URL != "" && !strings.HasPrefix(d.SHA256URL, "https://") {
			return fmt.Errorf("download %d checksum URL must be https", i)
		}
	}
	return nil
}

// expandURL substitutes {{VAR}} from env. Values must look like versions so
// a user-editable field cannot redirect the download elsewhere.
func expandURL(raw string, env map[string]string) (string, error) {
	var bad error
	out := urlVarPattern.ReplaceAllStringFunc(raw, func(m string) string {
		key := m[2 : len(m)-2]
		val := strings.TrimSpace(env[key])
		if !safeVersionPattern.MatchString(val) {
			bad = fmt.Errorf("%s value %q is not a valid version", key, val)
			return ""
		}
		return url.PathEscape(val)
	})
	return out, bad
}

type githubAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

func resolveGithubAsset(ctx context.Context, repo, pattern, version string) (githubAsset, string, error) {
	api := "https://api.github.com/repos/" + repo + "/releases/latest"
	if version != "" && version != "latest" {
		api = "https://api.github.com/repos/" + repo + "/releases/tags/" + url.PathEscape(version)
	}
	body, _, err := fetchURL(ctx, defaultHTTPClient(), api)
	if err != nil {
		return githubAsset{}, "", fmt.Errorf("could not read %s release %s: %w", repo, firstNonEmpty(version, "latest"), err)
	}
	var rel struct {
		Tag    string        `json:"tag_name"`
		Assets []githubAsset `json:"assets"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return githubAsset{}, "", fmt.Errorf("%s release metadata is not valid JSON", repo)
	}
	re := regexp.MustCompile(pattern)
	for _, a := range rel.Assets {
		if re.MatchString(a.Name) {
			return a, rel.Tag, nil
		}
	}
	var names []string
	for _, a := range rel.Assets {
		names = append(names, a.Name)
	}
	return githubAsset{}, rel.Tag, fmt.Errorf("%s release %s has no asset matching %s (assets: %s)", repo, rel.Tag, pattern, clip(strings.Join(names, ", "), 300))
}

func nativeDownloads(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	if err := validateDownloadTemplate(t); err != nil {
		return err
	}
	env := map[string]string{}
	for k, v := range srv.Env {
		env[k] = v
	}
	version := strings.TrimSpace(env[t.versionEnv()])
	if version != "" && version != "latest" && !safeVersionPattern.MatchString(version) {
		return fmt.Errorf("version %q is not valid", version)
	}
	stage := filepath.Join(dir, ".ndl-download")
	if err := os.MkdirAll(stage, 0o750); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for i, d := range t.Install.Downloads {
		var src, name string
		var want digest
		if d.Repo != "" && d.URL != "" && version != "" && version != "latest" {
			// Pinned release: fetch the known asset URL directly. GitHub
			// publishes no checksum outside the API, so none is verified.
			expanded, err := expandURL(d.URL, env)
			if err != nil {
				return err
			}
			src, name = expanded, pathBase(expanded)
			r.progress(srv.ID, "downloading", fmt.Sprintf("%s %s: %s", d.Repo, version, name))
		} else if d.Repo != "" {
			asset, tag, err := resolveGithubAsset(ctx, d.Repo, d.Asset, version)
			if err != nil {
				return err
			}
			src, name = asset.URL, asset.Name
			if strings.HasPrefix(asset.Digest, "sha256:") {
				want = digest{Algo: "sha256", Hex: strings.TrimPrefix(asset.Digest, "sha256:")}
			}
			r.progress(srv.ID, "downloading", fmt.Sprintf("%s %s: %s", d.Repo, tag, name))
		} else {
			expanded, err := expandURL(d.URL, env)
			if err != nil {
				return err
			}
			src = expanded
			name = pathBase(strings.SplitN(expanded, "?", 2)[0])
			if d.SHA256URL != "" {
				sumURL, err := expandURL(d.SHA256URL, env)
				if err != nil {
					return err
				}
				want, err = fetchPublishedSHA256(ctx, sumURL, name)
				if err != nil {
					return err
				}
			}
			r.progress(srv.ID, "downloading", "fetching "+src)
		}
		kind := archiveKind(d, name)
		if kind == "file" && d.Dest != "" {
			name = d.Dest
		}
		if name == "" || name == "." {
			name = "download-" + strconv.Itoa(i)
		}
		staged := filepath.Join(stage, strconv.Itoa(i)+"-"+safeContentName(name))
		if err := fetchFileRetry(ctx, src, staged, maxBinaryBytes, want, 3); err != nil {
			return fmt.Errorf("download of %s failed: %w", name, err)
		}
		if want.Hex != "" {
			r.appendLog(srv.ID, "verified "+want.Algo+" checksum for "+name)
		}
		dest := dir
		if d.Subdir != "" {
			rel, _ := cleanArchiveRel(d.Subdir)
			dest = filepath.Join(dir, filepath.FromSlash(rel))
		}
		if err := os.MkdirAll(dest, 0o750); err != nil {
			return err
		}
		r.progress(srv.ID, "extracting", "unpacking "+name)
		kept, err := preserveFiles(dest, d.Keep)
		if err != nil {
			return err
		}
		extractErr := extractDownload(ctx, staged, dest, kind, d.Strip, name)
		if err := restoreFiles(dest, kept); err != nil {
			return err
		}
		if extractErr != nil {
			return extractErr
		}
		for _, exe := range d.Executables {
			rel, err := cleanArchiveRel(exe)
			if err != nil {
				return err
			}
			_ = os.Chmod(filepath.Join(dest, filepath.FromSlash(rel)), 0o755)
		}
	}
	r.appendLog(srv.ID, "installed "+t.Name+" files")
	return nil
}

func extractDownload(ctx context.Context, src, dest, kind string, strip int, name string) error {
	switch kind {
	case "file":
		return os.Rename(src, filepath.Join(dest, safeContentName(name)))
	case "zip":
		return unzipStrip(src, dest, strip)
	case "tar", "tar.gz", "tar.xz", "tar.bz2":
		flag := map[string]string{"tar": "-xf", "tar.gz": "-xzf", "tar.xz": "-xJf", "tar.bz2": "-xjf"}[kind]
		args := []string{flag, src, "-C", dest, "--no-same-owner", "--no-same-permissions"}
		if strip > 0 {
			args = append(args, "--strip-components="+strconv.Itoa(strip))
		}
		out, err := exec.CommandContext(ctx, "tar", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("extract %s failed: %s", name, strings.TrimSpace(string(out)+" "+err.Error()))
		}
		return nil
	}
	return fmt.Errorf("archive type %s is not supported", kind)
}

// unzipStrip extracts a zip, dropping strip leading path components and
// refusing entries that would escape dest. File modes from the archive are
// kept for executables.
func unzipStrip(src, dest string, strip int) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("archive is not a valid zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		parts := strings.Split(strings.Trim(name, "/"), "/")
		if len(parts) <= strip {
			continue
		}
		rel := strings.Join(parts[strip:], "/")
		if f.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			if clean, err := cleanArchiveRel(rel); err == nil {
				_ = os.MkdirAll(filepath.Join(dest, filepath.FromSlash(clean)), 0o750)
			}
			continue
		}
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := writeZipFile(dest, rel, f); err != nil {
			return err
		}
		if f.FileInfo().Mode()&0o111 != 0 {
			clean, _ := cleanArchiveRel(rel)
			_ = os.Chmod(filepath.Join(dest, filepath.FromSlash(clean)), 0o755)
		}
	}
	return nil
}

// downloadsPlan is the container fallback used when native install is not
// available. It mirrors nativeDownloads with curl and tar.
func downloadsPlan(t Template) (string, string) {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n")
	b.WriteString("if ! command -v curl >/dev/null || ! command -v unzip >/dev/null || ! command -v xz >/dev/null || ! command -v bzip2 >/dev/null; then\n  apt-get update\n  apt-get install -y --no-install-recommends curl ca-certificates unzip xz-utils bzip2\nfi\n")
	b.WriteString("cd /mnt/server\n")
	fmt.Fprintf(&b, "VER=\"${%s:-latest}\"\n", t.versionEnv())
	b.WriteString("UA=\"No-DAL-GameServers/1.0\"\nmkdir -p .ndl-download\n")
	if t.Install == nil {
		return b.String(), "debian:bookworm-slim"
	}
	for i, d := range t.Install.Downloads {
		file := fmt.Sprintf(".ndl-download/%d.bin", i)
		if d.Repo != "" {
			fmt.Fprintf(&b, "if [ \"$VER\" = latest ]; then API=https://api.github.com/repos/%s/releases/latest; else API=https://api.github.com/repos/%s/releases/tags/$VER; fi\n", d.Repo, d.Repo)
			fmt.Fprintf(&b, "URL=$(curl -fsSL -A \"$UA\" \"$API\" | grep -o '\"browser_download_url\": *\"[^\"]*\"' | sed 's/.*\"\\(http[^\"]*\\)\"/\\1/' | grep -E %s | head -n 1)\n", shellQuote(strings.TrimSuffix(strings.TrimPrefix(d.Asset, "^"), "$")))
		} else {
			u := urlVarPattern.ReplaceAllString(d.URL, "$${$1}")
			fmt.Fprintf(&b, "URL=\"%s\"\n", u)
		}
		b.WriteString("test -n \"$URL\"\n")
		fmt.Fprintf(&b, "curl -fL --retry 3 -A \"$UA\" \"$URL\" -o %s\n", file)
		dest := "."
		if d.Subdir != "" {
			dest = d.Subdir
			fmt.Fprintf(&b, "mkdir -p %s\n", shellQuote(dest))
		}
		strip := ""
		if d.Strip > 0 {
			strip = fmt.Sprintf(" --strip-components=%d", d.Strip)
		}
		name := d.Dest
		if name == "" && d.URL != "" {
			name = pathBase(d.URL)
		}
		switch archiveKind(d, firstNonEmpty(name, d.Asset)) {
		case "zip":
			fmt.Fprintf(&b, "unzip -o %s -d %s\n", file, shellQuote(dest))
		case "tar.gz":
			fmt.Fprintf(&b, "tar -xzf %s -C %s%s\n", file, shellQuote(dest), strip)
		case "tar.xz":
			fmt.Fprintf(&b, "tar -xJf %s -C %s%s\n", file, shellQuote(dest), strip)
		case "tar.bz2":
			fmt.Fprintf(&b, "tar -xjf %s -C %s%s\n", file, shellQuote(dest), strip)
		default:
			fmt.Fprintf(&b, "mv %s %s/%s\n", file, shellQuote(dest), shellQuote(firstNonEmpty(d.Dest, "server.bin")))
		}
		for _, exe := range d.Executables {
			fmt.Fprintf(&b, "chmod +x %s/%s || true\n", shellQuote(dest), shellQuote(exe))
		}
	}
	b.WriteString("rm -rf .ndl-download\necho \"installed files\"\n")
	return b.String(), "debian:bookworm-slim"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// preserveFiles snapshots existing files listed in keep so an update can
// put them back after extraction.
func preserveFiles(dest string, keep []string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, k := range keep {
		rel, err := cleanArchiveRel(k)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err == nil {
			out[rel] = raw
		}
	}
	return out, nil
}

func restoreFiles(dest string, kept map[string][]byte) error {
	for rel, raw := range kept {
		if err := os.WriteFile(filepath.Join(dest, filepath.FromSlash(rel)), raw, 0o640); err != nil {
			return err
		}
	}
	return nil
}
