package lxc

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/guestextras"
)

// cloudflared is installed from Cloudflare's signed apt repository, the
// same way the Cloudflare feature installs it, so a connector the feature
// adds later finds a package-managed /usr/bin/cloudflared it can update.
const (
	guestCloudflared        = "/usr/bin/cloudflared"
	guestCloudflareKeyURL   = "https://pkg.cloudflare.com/cloudflare-main.gpg"
	guestCloudflareKeyring  = "/usr/share/keyrings/cloudflare-main.gpg"
	guestCloudflareList     = "/etc/apt/sources.list.d/cloudflared.list"
	guestCloudflareRepoLine = "deb [signed-by=" + guestCloudflareKeyring + "] https://pkg.cloudflare.com/cloudflared any main\n"
)

func (e *Engine) guestCloudflaredVersion(ctx context.Context, id string) (string, bool) {
	out, err := e.attach(ctx, id, guestCloudflared, "--version")
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(string(out))
	if f := strings.Fields(v); len(f) >= 3 {
		v = f[2]
	}
	return v, true
}

// installGuestCloudflared adds Cloudflare's repository and installs
// cloudflared. It does not create a tunnel: that stays with the Cloudflare
// page or the user.
func (e *Engine) installGuestCloudflared(ctx context.Context, id, family string, pkgs []string, mode extraMode) (ExtraChange, error) {
	change := ExtraChange{Extra: guestextras.Cloudflared}
	before, had := e.guestCloudflaredVersion(ctx, id)
	if had && mode == extraInstall {
		change.Outcome, change.Version = ChangeCurrent, before
		return change, nil
	}
	if family != "debian" {
		return change, fmt.Errorf("cloudflared is installed from Cloudflare's apt repository, which needs a Debian-based container")
	}
	if err := e.ensureGuestCurl(ctx, id, family, pkgs); err != nil {
		return change, err
	}
	if _, err := e.attach(ctx, id, "/bin/mkdir", "-p", "-m", "0755", "/usr/share/keyrings"); err != nil {
		return change, fmt.Errorf("could not create /usr/share/keyrings: %s", sanitizeGuestSetupErr(err))
	}
	if out, err := e.attach(ctx, id, guestCurl, "-fsS", "-L", "--proto", "=https", "--retry", "3", "--connect-timeout", "15",
		"--max-time", "120", "-o", guestCloudflareKeyring, guestCloudflareKeyURL); err != nil {
		return change, fmt.Errorf("could not download Cloudflare's package key: %s", sanitizeGuestSetupErr(aptError(out, err)))
	}
	if err := e.writeGuestFile(ctx, id, guestCloudflareList, guestCloudflareRepoLine); err != nil {
		return change, fmt.Errorf("could not add Cloudflare's package source: %s", sanitizeGuestSetupErr(err))
	}
	if err := e.aptRetry(ctx, id, "update"); err != nil {
		return change, fmt.Errorf("package index update failed: %s", sanitizeGuestSetupErr(err))
	}
	args := aptModeArgs(mode, []string{"cloudflared"})
	if mode != extraInstall && !had {
		args = aptModeArgs(extraInstall, []string{"cloudflared"})
	}
	if err := e.aptRetry(ctx, id, args...); err != nil {
		return change, fmt.Errorf("could not install cloudflared: %s", sanitizeGuestSetupErr(err))
	}
	version, ok := e.guestCloudflaredVersion(ctx, id)
	if !ok {
		return change, fmt.Errorf("cloudflared was installed but does not run")
	}
	change.Outcome, change.Version = ChangeInstalled, version
	if had {
		change.Outcome = ChangeUpdated
		if version == before {
			change.Outcome = ChangeCurrent
		}
	}
	return change, nil
}

// writeGuestFile writes a small file in the guest through stdin.
func (e *Engine) writeGuestFile(ctx context.Context, id, path, content string) error {
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	go func() {
		_, _ = w.WriteString(content)
		_ = w.Close()
	}()
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = e.runStdin(wctx, r, BinLXCAttach, e.attachArgs(id, "/bin/dd", "of="+path, "status=none")...)
	_ = r.Close()
	return err
}
