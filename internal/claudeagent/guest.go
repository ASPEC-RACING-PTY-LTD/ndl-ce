package claudeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// GuestRunner runs one fixed argv inside the agent container as root.
type GuestRunner func(ctx context.Context, argv ...string) ([]byte, error)

// Guest drives Claude Code inside the agent container. Every command is a
// fixed argv; values (names, codes, file contents) are passed as arguments,
// never spliced into a shell string.
type Guest struct {
	Run GuestRunner
	// Sleep waits between polls; tests replace it.
	Sleep func(time.Duration)
}

const guestPath = GuestHome + "/.local/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// setupScript installs the tools, the claude user, Claude Code, the
// Remote Control service and the ndl command. It is safe to run again.
const setupScript = `set -eu
export DEBIAN_FRONTEND=noninteractive
need=
for c in tmux curl jq git rg runuser; do command -v "$c" >/dev/null 2>&1 || need=1; done
if [ -n "$need" ]; then
  apt-get update -q
  apt-get install -y -q tmux curl jq git ripgrep ca-certificates procps util-linux
fi
if ! id claude >/dev/null 2>&1; then
  if getent passwd 1000 >/dev/null; then
    echo "user id 1000 is already used by $(getent passwd 1000 | cut -d: -f1)" >&2
    exit 1
  fi
  useradd -m -u 1000 -s /bin/bash claude
fi
mkdir -p /srv /etc/ndl-claude /usr/local/lib/ndl-claude
chown claude:claude /srv
if [ ! -x /home/claude/.local/bin/claude ]; then
  runuser -u claude -- env HOME=/home/claude PATH=/usr/local/bin:/usr/bin:/bin sh -c 'curl -fsSL https://claude.ai/install.sh | bash'
fi
test -x /home/claude/.local/bin/claude
f=/home/claude/.claude.json
[ -s "$f" ] || echo '{}' > "$f"
jq '.hasCompletedOnboarding = true | .projects["/srv"].hasTrustDialogAccepted = true' "$f" > "$f.tmp"
mv "$f.tmp" "$f"
chown claude:claude "$f"
chmod 600 "$f"
cat > /usr/local/lib/ndl-claude/logged-in <<'EOF'
#!/bin/sh
claude auth status --json 2>/dev/null | grep -q '"loggedIn": *true'
EOF
cat > /usr/local/lib/ndl-claude/run <<'EOF'
#!/bin/sh
exec claude remote-control --name "$NDL_RC_NAME" --permission-mode "$NDL_RC_MODE" </dev/null
EOF
chmod 0755 /usr/local/lib/ndl-claude/logged-in /usr/local/lib/ndl-claude/run
cat > /etc/systemd/system/ndl-claude.service <<'EOF'
[Unit]
Description=No-dal Claude Agent (Claude Code Remote Control)
After=network-online.target
Wants=network-online.target

[Service]
User=claude
Group=claude
WorkingDirectory=/srv
Environment=HOME=/home/claude
Environment=PATH=/home/claude/.local/bin:/usr/local/bin:/usr/bin:/bin
EnvironmentFile=/etc/ndl-claude/env
ExecCondition=/usr/local/lib/ndl-claude/logged-in
ExecStart=/usr/local/lib/ndl-claude/run
Restart=on-failure
RestartSec=15

[Install]
WantedBy=multi-user.target
EOF
[ -f /etc/ndl-claude/env ] || printf 'NDL_RC_NAME="No-dal"\nNDL_RC_MODE=default\n' > /etc/ndl-claude/env
cat > /usr/local/bin/ndl <<'EOF'
#!/bin/sh
# ndl: the No-dal actions this Claude Agent was granted. See /srv/CLAUDE.md.
set -eu
cfg=/etc/ndl-claude/api.json
if [ ! -r "$cfg" ]; then
  echo "ndl: this agent has no No-dal access configured yet" >&2
  exit 1
fi
url=$(jq -r .url "$cfg")
token=$(jq -r .token "$cfg")
# call prints the reply, or the API's error on stderr and fails.
call() {
  method=$1
  route=$2
  shift 2
  if ! body=$(curl -sS --fail-with-body -X "$method" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' "$url/api/v1/claude-agent/api$route" "$@"); then
    msg=$(printf '%s' "$body" | jq -r '.error // empty' 2>/dev/null || true)
    echo "ndl: ${msg:-request failed}" >&2
    exit 1
  fi
  printf '%s\n' "$body"
}
usage() {
  echo "usage: ndl servers | ndl log SERVER [LINES] | ndl console SERVER COMMAND... | ndl restart SERVER" >&2
  exit 2
}
cmd=${1:-}
[ $# -gt 0 ] && shift
case "$cmd" in
  servers) out=$(call GET /servers); printf '%s\n' "$out" | jq . ;;
  log) [ $# -ge 1 ] || usage; out=$(call GET "/servers/$1/log?lines=${2:-200}"); printf '%s\n' "$out" | jq -r .log ;;
  console) [ $# -ge 2 ] || usage; srv=$1; shift; out=$(call POST "/servers/$srv/console" --data "$(jq -nc --arg c "$*" '{command:$c}')"); printf '%s\n' "$out" | jq -r .message ;;
  restart) [ $# -ge 1 ] || usage; out=$(call POST "/servers/$1/restart"); printf '%s\n' "$out" | jq -r .message ;;
  *) usage ;;
esac
EOF
chmod 0755 /usr/local/bin/ndl
systemctl daemon-reload
systemctl enable ndl-claude.service >/dev/null 2>&1 || true
`

// writeFileScript writes $2 to $1 atomically with mode $3 and owner $4.
const writeFileScript = `set -eu
umask 077
printf '%s' "$2" > "$1.ndl-tmp"
chmod "$3" "$1.ndl-tmp"
chown "$4" "$1.ndl-tmp"
mv "$1.ndl-tmp" "$1"`

var loginURLRe = regexp.MustCompile(`https://\S+/oauth/authorize\?\S+`)

func (g Guest) sleep(d time.Duration) {
	if g.Sleep != nil {
		g.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (g Guest) asClaude(ctx context.Context, argv ...string) ([]byte, error) {
	return g.Run(ctx, append([]string{"runuser", "-u", GuestUser, "--", "env", "HOME=" + GuestHome, "PATH=" + guestPath}, argv...)...)
}

// Setup installs everything; it takes several minutes on a fresh container.
func (g Guest) Setup(ctx context.Context) error {
	if out, err := g.Run(ctx, "sh", "-c", setupScript); err != nil {
		return fmt.Errorf("setting up Claude Code failed: %v: %s", err, tail(string(out), 600))
	}
	return nil
}

// Configure writes the Remote Control settings, the API access for ndl and
// CLAUDE.md. It does not restart anything.
func (g Guest) Configure(ctx context.Context, req Request) error {
	name := firstNonEmpty(req.Name, "No-dal")
	if err := ValidName(name); err != nil {
		return err
	}
	mode := firstNonEmpty(req.PermissionMode, "default")
	if err := ValidMode(mode); err != nil {
		return err
	}
	api, _ := json.Marshal(map[string]string{"url": req.APIURL, "token": req.APIToken})
	files := []struct{ path, body, mode, owner string }{
		{ConfigDir + "/env", fmt.Sprintf("NDL_RC_NAME=%q\nNDL_RC_MODE=%s\n", name, mode), "0644", "root:root"},
		{ConfigDir + "/api.json", string(api) + "\n", "0600", GuestUser + ":" + GuestUser},
		{WorkDir + "/CLAUDE.md", ClaudeMD(req.Grants), "0644", "root:root"},
	}
	for _, f := range files {
		if out, err := g.Run(ctx, "sh", "-c", writeFileScript, "sh", f.path, f.body, f.mode, f.owner); err != nil {
			return fmt.Errorf("writing %s: %v: %s", f.path, err, tail(string(out), 300))
		}
	}
	return nil
}

// Status reports Claude Code's install, sign-in and Remote Control state.
func (g Guest) Status(ctx context.Context) Status {
	var st Status
	if _, err := g.Run(ctx, "test", "-x", ClaudeBin); err != nil {
		st.Message = "Claude Code is not installed yet"
		return st
	}
	st.Installed = true
	if out, err := g.asClaude(ctx, ClaudeBin, "--version"); err == nil {
		if f := strings.Fields(string(out)); len(f) > 0 {
			st.Version = f[0]
		}
	}
	if out, err := g.asClaude(ctx, ClaudeBin, "auth", "status", "--json"); err == nil || len(out) > 0 {
		parseAuth(out, &st)
	}
	out, _ := g.Run(ctx, "systemctl", "is-active", Service)
	st.Service = strings.TrimSpace(string(out))
	return st
}

// parseAuth reads claude auth status --json. Only the sign-in state, the
// account's email and the plan are kept.
func parseAuth(raw []byte, st *Status) {
	i := strings.IndexByte(string(raw), '{')
	if i < 0 {
		return
	}
	var m map[string]any
	if json.Unmarshal(raw[i:], &m) != nil {
		return
	}
	st.LoggedIn, _ = m["loggedIn"].(bool)
	for _, k := range []string{"email", "emailAddress", "account"} {
		switch v := m[k].(type) {
		case string:
			st.Account = v
		case map[string]any:
			if e, ok := v["email"].(string); ok {
				st.Account = e
			}
		}
		if st.Account != "" {
			break
		}
	}
	for _, k := range []string{"subscriptionType", "plan"} {
		if v, ok := m[k].(string); ok && v != "" {
			st.Plan = v
			break
		}
	}
}

// LoginStart starts claude auth login in a tmux session and returns the
// sign-in link it prints.
func (g Guest) LoginStart(ctx context.Context) (string, error) {
	_, _ = g.asClaude(ctx, "tmux", "kill-session", "-t", LoginSession)
	if out, err := g.asClaude(ctx, "env", "BROWSER=/bin/false", "tmux", "new-session", "-d", "-s", LoginSession, "-x", "1000", "-y", "50",
		ClaudeBin, "auth", "login", "--claudeai"); err != nil {
		return "", fmt.Errorf("starting the sign-in failed: %v: %s", err, tail(string(out), 300))
	}
	for range 30 {
		g.sleep(time.Second)
		out, err := g.asClaude(ctx, "tmux", "capture-pane", "-p", "-J", "-t", LoginSession)
		if err != nil {
			continue
		}
		if u := loginURLRe.FindString(string(out)); u != "" {
			return u, nil
		}
	}
	return "", errors.New("Claude Code did not print a sign-in link; check that the container can reach claude.ai")
}

// LoginCode types the code from the sign-in page into the waiting login
// and waits until Claude Code reports it is signed in.
func (g Guest) LoginCode(ctx context.Context, code string) (Status, error) {
	code = strings.TrimSpace(code)
	if err := ValidCode(code); err != nil {
		return Status{}, err
	}
	if _, err := g.asClaude(ctx, "tmux", "has-session", "-t", LoginSession); err != nil {
		return Status{}, errors.New("the sign-in expired; get a new sign-in link")
	}
	if _, err := g.asClaude(ctx, "tmux", "send-keys", "-t", LoginSession, "-l", code); err != nil {
		return Status{}, fmt.Errorf("typing the code failed: %v", err)
	}
	if _, err := g.asClaude(ctx, "tmux", "send-keys", "-t", LoginSession, "Enter"); err != nil {
		return Status{}, fmt.Errorf("typing the code failed: %v", err)
	}
	var st Status
	for range 30 {
		g.sleep(2 * time.Second)
		st = g.Status(ctx)
		if st.LoggedIn {
			_, _ = g.asClaude(ctx, "tmux", "kill-session", "-t", LoginSession)
			_ = g.Restart(ctx)
			st.Service = "activating"
			return st, nil
		}
		if _, err := g.asClaude(ctx, "tmux", "has-session", "-t", LoginSession); err != nil {
			break
		}
	}
	out, _ := g.asClaude(ctx, "tmux", "capture-pane", "-p", "-J", "-t", LoginSession)
	_, _ = g.asClaude(ctx, "tmux", "kill-session", "-t", LoginSession)
	msg := "the sign-in did not finish; get a new sign-in link and try again"
	if line := lastLine(string(out)); line != "" {
		msg += " (Claude Code said: " + line + ")"
	}
	return st, errors.New(msg)
}

// Logout signs Claude Code out and stops Remote Control.
func (g Guest) Logout(ctx context.Context) error {
	_, _ = g.Run(ctx, "systemctl", "stop", Service)
	if out, err := g.asClaude(ctx, ClaudeBin, "auth", "logout"); err != nil {
		return fmt.Errorf("signing out failed: %v: %s", err, tail(string(out), 300))
	}
	return nil
}

// Restart restarts Remote Control. Its condition skips it while signed out.
func (g Guest) Restart(ctx context.Context) error {
	if out, err := g.Run(ctx, "systemctl", "restart", Service); err != nil {
		return fmt.Errorf("restarting Remote Control failed: %v: %s", err, tail(string(out), 300))
	}
	return nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "..." + s[len(s)-n:]
	}
	return s
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && !strings.HasPrefix(l, "Paste code") {
			return tail(l, 200)
		}
	}
	return ""
}
