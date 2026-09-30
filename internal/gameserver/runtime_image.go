package gameserver

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var debianPackagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+(:i386|:amd64)?$`)

// RuntimeImageTag is the local tag for base plus a sorted package set.
func RuntimeImageTag(base string, pkgs []string) string {
	sorted := append([]string(nil), pkgs...)
	sort.Strings(sorted)
	return "ndl-gs-runtime:" + shortHash(base+"|"+strings.Join(sorted, ","))
}

// EnsureRuntimeImage layers Debian packages onto a base image once per node
// and returns the local tag. It uses docker run plus docker commit so no
// build context or Dockerfile is written to disk. Package names are
// validated so a template cannot smuggle shell into the build step.
func (r *Runtime) EnsureRuntimeImage(ctx context.Context, base string, pkgs []string) (string, error) {
	if len(pkgs) == 0 {
		return base, nil
	}
	if !validImageRef(base) {
		return "", fmt.Errorf("runtime base image %q is not valid", base)
	}
	for _, p := range pkgs {
		if !debianPackagePattern.MatchString(p) {
			return "", fmt.Errorf("runtime dependency %q is not a valid Debian package name", p)
		}
	}
	tag := RuntimeImageTag(base, pkgs)
	r.imageMu.Lock()
	defer r.imageMu.Unlock()
	if _, err := r.run(ctx, r.dockerBin(), "image", "inspect", "--format", "{{.Id}}", tag); err == nil {
		return tag, nil
	}
	sorted := append([]string(nil), pkgs...)
	sort.Strings(sorted)
	needI386 := false
	for _, p := range sorted {
		if strings.HasSuffix(p, ":i386") {
			needI386 = true
		}
	}
	script := "set -e; export DEBIAN_FRONTEND=noninteractive; "
	if needI386 {
		script += "dpkg --add-architecture i386; "
	}
	script += "apt-get update; apt-get install -y --no-install-recommends " + strings.Join(sorted, " ") + "; rm -rf /var/lib/apt/lists/*"
	name := "ndl-gs-build-" + strings.TrimPrefix(tag, "ndl-gs-runtime:")
	_, _ = r.run(ctx, r.dockerBin(), "rm", "-f", name)
	args := []string{"run", "--name", name}
	args = append(args, r.networkArgs()...)
	args = append(args, "--user", "0:0", "--entrypoint", "/bin/sh", base, "-c", script)
	out, err := r.run(ctx, r.dockerBin(), args...)
	if err != nil {
		_, _ = r.run(ctx, r.dockerBin(), "rm", "-f", name)
		return "", fmt.Errorf("%s", strings.TrimSpace(clip(string(out), 2000)+" "+err.Error()))
	}
	out, err = r.run(ctx, r.dockerBin(), "commit", "--change", "ENTRYPOINT []", "--change", "CMD []", name, tag)
	_, _ = r.run(ctx, r.dockerBin(), "rm", "-f", name)
	if err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(out)+" "+err.Error()))
	}
	r.appendLog("", "built runtime image "+tag+" from "+base)
	return tag, nil
}
