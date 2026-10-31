package agentrpc

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

// TargetGameServer is a game server's live console: its log as it is
// written and a line input that goes to the server's stdin.
const TargetGameServer = "gameserver"

// gameConsoleScript runs in a PTY. It prints the last lines of the log,
// follows it, and follows the next container after a restart. Each line
// typed (the PTY gives line editing and echo) is written to the server's
// stdin, the same way console commands from the API are. Ctrl+C clears the
// line and never stops the server.
//
// $1 is the container name and $2 the docker binary; neither is
// interpolated into the script.
const gameConsoleScript = `trap '' INT QUIT TSTP
trap 'trap - HUP TERM EXIT; kill 0' HUP TERM EXIT
c="$1"; d="$2"
(
  tail=300
  stopped=""
  while :; do
    if "$d" inspect "$c" >/dev/null 2>&1; then
      stopped=""
      "$d" logs -f --tail "$tail" "$c" 2>&1
      tail=all
    elif [ -z "$stopped" ]; then
      printf '\033[33m[server is not running]\033[0m\n'
      stopped=1
      tail=all
    fi
    sleep 2
  done
) &
while IFS= read -r line; do
  [ -n "$line" ] || continue
  if ! printf '%s\n' "$line" | "$d" exec -i "$c" /bin/sh -c 'cat > /proc/1/fd/0' 2>/dev/null; then
    printf '\033[31m[not sent: the server is not running]\033[0m\n'
  fi
done
`

func gameConsoleArgv(id string) ([]string, error) {
	id = strings.TrimSpace(id)
	if !gameserver.ValidHostID(id) {
		return nil, fmt.Errorf("game server id is invalid")
	}
	docker := strings.TrimSpace(os.Getenv("NDL_GS_DOCKER"))
	if docker == "" {
		docker = "/usr/bin/docker"
	}
	return []string{"/bin/sh", "-c", gameConsoleScript, "ndl-game-console", "ndl-gs-" + id, docker}, nil
}

// TargetGameServerShell is a shell inside a running game server's container,
// starting in the server folder.
const TargetGameServerShell = "gameserver-shell"

func startGameServerShell(ctx context.Context, h *Handler, req termRequest) (termSession, error) {
	id := strings.TrimSpace(req.TargetID)
	if !gameserver.ValidHostID(id) {
		return nil, fmt.Errorf("game server id is invalid")
	}
	sess, err := h.docker().Exec(ctx, docker.ExecRequest{MachineID: docker.HostMachineID, ContainerID: "ndl-gs-" + id, WorkDir: "/home/container"})
	if err != nil {
		return nil, fmt.Errorf("the game server is not running: %w", err)
	}
	return sess, nil
}
