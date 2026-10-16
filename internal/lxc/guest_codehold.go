package lxc

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/guestextras"
)

// The CodeHold CLI ("ch") is a single static binary served by the CodeHold
// server at /downloads/ch-linux-<arch>. It is downloaded inside the guest
// with curl straight to a temporary file next to its final place, checked
// with "ch --version" and only then renamed over /usr/local/bin/ch, so a
// failed or partial download never replaces a working ch. The server's
// install script (/install/ch.sh) is not used: No-dal runs fixed argv lists
// in guests and never a downloaded script as root, and the CPU type is
// already known from the image pin.
//
// This step only installs the binary. Signing in ("ch auth login") is done by
// each user, and CodeHold Actions runners are a separate integration.
const (
	guestCHPath     = "/usr/local/bin/ch"
	guestCHTemp     = "/usr/local/bin/.ch.ndl-new"
	guestCACerts    = "/etc/ssl/certs/ca-certificates.crt"
	guestCurl       = "/usr/bin/curl"
	chDownloadLimit = "600"
)

// Outcomes reported for a download extra in Result.Changes.
const (
	ChangeInstalled = "installed"
	ChangeUpdated   = "updated"
	ChangeCurrent   = "current"
)

// CodeholdSource says where the CodeHold CLI is downloaded from.
type CodeholdSource struct {
	// URL is the CodeHold server, for example https://git.example.com.
	URL string `json:"url"`
	// AccessClientID and AccessClientSecret are an optional Cloudflare Access
	// service token for a CodeHold behind Access. They are written to a 0600
	// header file in the guest for the one curl call and removed after it;
	// they never appear on a command line or in a log. The control plane
	// leaves them empty today (see codeholdCLIAccess in httpapi).
	AccessClientID     string `json:"access_client_id,omitempty"`
	AccessClientSecret string `json:"access_client_secret,omitempty"`
}

// CHDownloadURL is the "ch" binary for one CPU type on a CodeHold server. It
// refuses anything but an https URL with a host.
func CHDownloadURL(server, arch string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(server))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("the CodeHold address must be an https URL")
	}
	switch arch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("ch is not built for %q", arch)
	}
	return strings.TrimRight(u.String(), "/") + "/downloads/ch-linux-" + arch, nil
}

// guestCHVersion is the output of "ch --version", or false when ch is
// missing or does not run.
func (e *Engine) guestCHVersion(ctx context.Context, id, path string) (string, bool) {
	out, err := e.attach(ctx, id, path, "--version")
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(string(out))
	if i := strings.IndexByte(v, '\n'); i >= 0 {
		v = v[:i]
	}
	return v, true
}

func (e *Engine) guestArch(ctx context.Context, id, pin string) string {
	if a := guestextras.Arch(pin); a != "" {
		return a
	}
	out, err := e.attach(ctx, id, "/bin/uname", "-m")
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(string(out)) {
	case "x86_64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return strings.TrimSpace(string(out))
}

// ensureGuestCurl installs curl and the CA bundle only when one is missing,
// so an install on a ready guest never touches the package manager.
func (e *Engine) ensureGuestCurl(ctx context.Context, id, family string, pkgs []string) error {
	_, curlErr := e.attach(ctx, id, guestCurl, "--version")
	_, caErr := e.attach(ctx, id, "/usr/bin/test", "-s", guestCACerts)
	if curlErr == nil && caErr == nil {
		return nil
	}
	switch family {
	case "debian":
		if err := e.aptRetry(ctx, id, "update"); err != nil {
			_ = e.repairDPKG(ctx, id)
			if err := e.aptRetry(ctx, id, "update"); err != nil {
				return fmt.Errorf("package index update failed: %s", sanitizeGuestSetupErr(err))
			}
		}
		args := append([]string{"install", "-y", "--no-install-recommends"}, pkgs...)
		if err := e.aptRetry(ctx, id, args...); err != nil {
			_ = e.repairDPKG(ctx, id)
			if err := e.aptRetry(ctx, id, args...); err != nil {
				return fmt.Errorf("could not install curl: %s", sanitizeGuestSetupErr(err))
			}
		}
	case "alpine":
		if err := e.apk(ctx, id, extraInstall, pkgs); err != nil {
			return fmt.Errorf("could not install curl: %s", sanitizeGuestSetupErr(err))
		}
	default:
		return fmt.Errorf("unsupported guest family %s", family)
	}
	return nil
}

// installGuestCodeholdCLI installs ch, or in update and reinstall mode
// replaces it with the server's current binary. An install where ch already
// runs changes nothing.
func (e *Engine) installGuestCodeholdCLI(ctx context.Context, id, pin, family string, pkgs []string, mode extraMode, src *CodeholdSource) (ExtraChange, error) {
	change := ExtraChange{Extra: guestextras.CodeholdCLI}
	before, had := e.guestCHVersion(ctx, id, guestCHPath)
	if had && mode == extraInstall {
		change.Outcome, change.Version = ChangeCurrent, before
		return change, nil
	}
	if src == nil || strings.TrimSpace(src.URL) == "" {
		return change, fmt.Errorf("no CodeHold address is configured")
	}
	arch := e.guestArch(ctx, id, pin)
	from, err := CHDownloadURL(src.URL, arch)
	if err != nil {
		return change, err
	}
	if err := e.ensureGuestCurl(ctx, id, family, pkgs); err != nil {
		return change, err
	}
	if _, err := e.attach(ctx, id, "/bin/mkdir", "-p", "/usr/local/bin"); err != nil {
		return change, fmt.Errorf("could not create /usr/local/bin: %s", sanitizeGuestSetupErr(err))
	}
	curl := []string{
		guestCurl, "-fsS", "-L", "--proto", "=https", "--proto-redir", "=https",
		"--retry", "3", "--retry-delay", "2", "--connect-timeout", "15", "--max-time", chDownloadLimit,
	}
	// Cloudflare Access hook: off unless the control plane passes a service
	// token. The headers go through a 0600 file read by curl -H @file.
	if src.AccessClientID != "" && src.AccessClientSecret != "" {
		hdr, err := e.writeGuestSecretFile(ctx, id,
			"CF-Access-Client-Id: "+src.AccessClientID+"\nCF-Access-Client-Secret: "+src.AccessClientSecret+"\n")
		if err != nil {
			return change, fmt.Errorf("could not prepare the Cloudflare Access headers")
		}
		defer func() { _, _ = e.attach(context.WithoutCancel(ctx), id, "/bin/rm", "-f", hdr) }()
		curl = append(curl, "-H", "@"+hdr)
	}
	curl = append(curl, "-o", guestCHTemp, from)
	removeTemp := func() { _, _ = e.attach(context.WithoutCancel(ctx), id, "/bin/rm", "-f", guestCHTemp) }
	if out, err := e.attach(ctx, id, curl...); err != nil {
		removeTemp()
		msg := sanitizeGuestSetupErr(aptError(out, err))
		log.Printf("container %s: codehold-cli: download from %s failed: %s", id, from, msg)
		return change, fmt.Errorf("could not download ch from %s: %s", from, msg)
	}
	if _, err := e.attach(ctx, id, "/bin/chmod", "0755", guestCHTemp); err != nil {
		removeTemp()
		return change, fmt.Errorf("could not make ch executable: %s", sanitizeGuestSetupErr(err))
	}
	version, ok := e.guestCHVersion(ctx, id, guestCHTemp)
	if !ok {
		removeTemp()
		return change, fmt.Errorf("the downloaded ch does not run (ch --version failed); the installed one is unchanged")
	}
	if had {
		if _, err := e.attach(ctx, id, "/usr/bin/cmp", "-s", guestCHTemp, guestCHPath); err == nil {
			removeTemp()
			change.Outcome, change.Version = ChangeCurrent, before
			return change, nil
		}
	}
	if _, err := e.attach(ctx, id, "/bin/mv", "-f", guestCHTemp, guestCHPath); err != nil {
		removeTemp()
		return change, fmt.Errorf("could not install ch: %s", sanitizeGuestSetupErr(err))
	}
	change.Outcome, change.Version = ChangeInstalled, version
	if had {
		change.Outcome = ChangeUpdated
	}
	log.Printf("container %s: codehold-cli: %s %s", id, change.Outcome, version)
	return change, nil
}

// writeGuestSecretFile writes content to a new 0600 file in the guest's /tmp
// through stdin, so it is never on a command line, and returns its path.
func (e *Engine) writeGuestSecretFile(ctx context.Context, id, content string) (string, error) {
	out, err := e.attach(ctx, id, "/bin/mktemp", "/tmp/.ndl-hdr.XXXXXXXX")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if !strings.HasPrefix(path, "/tmp/.ndl-hdr.") || strings.ContainsAny(path, " \n") {
		return "", fmt.Errorf("unexpected temporary file name")
	}
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	go func() {
		_, _ = w.WriteString(content)
		_ = w.Close()
	}()
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = e.runStdin(wctx, r, BinLXCAttach, e.attachArgs(id, "/bin/dd", "of="+path, "status=none")...)
	_ = r.Close()
	if err != nil {
		_, _ = e.attach(context.WithoutCancel(ctx), id, "/bin/rm", "-f", path)
		return "", err
	}
	return path, nil
}
