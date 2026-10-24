package httpapi

import (
	"net/http"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// ioTargetGameServer is a game server's live console session.
const ioTargetGameServer = "gameserver"

// ioSessionGrant is the permission a terminal session needs.
func ioSessionGrant(row appdb.IOSession) string {
	switch {
	case row.TargetKind == ioTargetGameServer:
		return rbac.GameServerConsole
	case row.Kind == appdb.IOKindConsole && row.TargetKind == appdb.IOTargetVM:
		return rbac.ComputeConsole
	}
	return rbac.TerminalOpen
}

// gameServerConsoleSession is POST /game-servers/{id}/console/sessions: a
// streamed console (the log as it is written, typed lines to the server's
// stdin) on the same socket the workload terminal uses.
func (s *Server) gameServerConsoleSession(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireGameFeature(w, r, rbac.GameServerConsole)
	if !ok {
		return
	}
	row, ok := s.loadGame(w, r, p, "console")
	if !ok {
		return
	}
	s.createTerminal(w, r, p, ioTargetGameServer, row.ID, "", "")
}
