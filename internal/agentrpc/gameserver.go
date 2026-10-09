package agentrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

// Game server actions. The control plane is unprivileged; these are the
// only game server operations it can ask the agent for. Each one is scoped
// to server_id and its folder under the agent data dir.
const (
	gsEnsureData      = "ensure-data"
	gsRemoveData      = "remove-data"
	gsInstall         = "install"
	gsInstallProgress = "install-progress"
	gsLogTail         = "log-tail"
	gsStart           = "start"
	gsStop            = "stop"
	gsStopGraceful    = "stop-graceful"
	gsKill            = "kill"
	gsRunning         = "running"
	gsSend            = "send"
	gsLogs            = "logs"
	gsListDir         = "list-dir"
	gsReadFile        = "read-file"
	gsWriteFile       = "write-file"
	gsMkdir           = "mkdir"
	gsRemove          = "remove"
	gsRename          = "rename"
	gsExtract         = "extract"
	gsBackup          = "backup"
	gsRestoreBackup   = "restore-backup"
	gsDeleteBackup    = "delete-backup"
	gsReady           = "runtime-ready"
)

// gameSpec carries the typed arguments for every game server action.
type gameSpec struct {
	Rel       string               `json:"rel,omitempty"`
	To        string               `json:"to,omitempty"`
	Max       int64                `json:"max,omitempty"`
	Tail      int                  `json:"tail,omitempty"`
	Line      string               `json:"line,omitempty"`
	StopCmd   string               `json:"stop_cmd,omitempty"`
	TimeoutMS int64                `json:"timeout_ms,omitempty"`
	BackupID  string               `json:"backup_id,omitempty"`
	Name      string               `json:"name,omitempty"`
	Server    *gameserver.Server   `json:"server,omitempty"`
	Template  *gameserver.Template `json:"template,omitempty"`
	Run       *gameserver.RunSpec  `json:"run,omitempty"`
}

// gameResult is the JSON result of a game server action.
type gameResult struct {
	Dir      string                 `json:"dir,omitempty"`
	ID       string                 `json:"id,omitempty"`
	Text     string                 `json:"text,omitempty"`
	Running  bool                   `json:"running,omitempty"`
	Phase    string                 `json:"phase,omitempty"`
	Message  string                 `json:"message,omitempty"`
	Data     []byte                 `json:"data,omitempty"`
	Listing  *gameserver.DirListing `json:"listing,omitempty"`
	Backup   *gameserver.BackupFile `json:"backup,omitempty"`
	NotFound bool                   `json:"not_found,omitempty"`
}

// gameProgress is the latest install phase per server, read by the
// control plane while an install RPC is running.
type gameProgress struct {
	phase, message, log string
}

var (
	gameHostOnce sync.Once
	gameHostInst *gameserver.LocalHost
	gameProgMu   sync.Mutex
	gameProgMap  = map[string]gameProgress{}
)

func (h *Handler) gameHost() *gameserver.LocalHost {
	if h.Games != nil {
		return h.Games
	}
	gameHostOnce.Do(func() {
		root := strings.TrimSpace(h.GameRoot)
		if root == "" {
			root = "/var/lib/ndl"
		}
		gameHostInst = gameserver.NewLocalHost(root)
	})
	return gameHostInst
}

func gameErr(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewError(connect.CodeFailedPrecondition, err)
}

func (h *Handler) execGameServer(ctx context.Context, m *agentv1.GameServer) (*connect.Response[agentv1.ExecuteResponse], error) {
	action := strings.TrimSpace(m.GetAction())
	id := strings.TrimSpace(m.GetServerId())
	if action == gsReady {
		if err := h.gameHost().Ready(ctx); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(gameResult{})}), nil
	}
	if !gameserver.ValidHostID(id) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("game server id is invalid"))
	}
	var spec gameSpec
	if len(m.GetSpecJson()) > 0 {
		if err := json.Unmarshal(m.GetSpecJson(), &spec); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("game server spec is invalid"))
		}
	}
	host := h.gameHost()
	var res gameResult
	var err error
	switch action {
	case gsEnsureData:
		res.Dir, err = host.EnsureData(ctx, id)
	case gsRemoveData:
		err = host.RemoveData(ctx, id)
	case gsInstall:
		if spec.Server == nil || spec.Template == nil || spec.Server.ID != id {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("install needs the server and template"))
		}
		setGameProgress(id, gameProgress{phase: "downloading"})
		err = host.Install(ctx, *spec.Server, *spec.Template, func(phase, message, logTail string) {
			setGameProgress(id, gameProgress{phase: phase, message: message, log: logTail})
		})
		clearGameProgress(id)
	case gsInstallProgress:
		gameProgMu.Lock()
		p, ok := gameProgMap[id]
		gameProgMu.Unlock()
		res.Running = ok
		res.Phase, res.Message, res.Text = p.phase, p.message, p.log
	case gsLogTail:
		res.Text = host.LogTail(ctx, id, spec.Tail)
	case gsStart:
		if spec.Run == nil || spec.Run.ID != id {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("start needs a run spec for this server"))
		}
		res.ID, err = host.Start(ctx, *spec.Run)
	case gsStop:
		err = host.Stop(ctx, id)
	case gsStopGraceful:
		err = host.StopGraceful(ctx, id, spec.StopCmd, time.Duration(spec.TimeoutMS)*time.Millisecond)
	case gsKill:
		err = host.Kill(ctx, id)
	case gsRunning:
		res.Running = host.Running(ctx, id)
	case gsSend:
		err = host.Send(ctx, id, spec.Line)
	case gsLogs:
		res.Text, err = host.Logs(ctx, id, spec.Tail)
	case gsListDir:
		var listing gameserver.DirListing
		listing, err = host.ListDir(ctx, id, spec.Rel)
		res.Listing = &listing
	case gsReadFile:
		res.Data, err = host.ReadFile(ctx, id, spec.Rel, spec.Max)
	case gsWriteFile:
		err = host.WriteFile(ctx, id, spec.Rel, m.GetData())
	case gsMkdir:
		err = host.Mkdir(ctx, id, spec.Rel)
	case gsRemove:
		err = host.Remove(ctx, id, spec.Rel)
	case gsRename:
		err = host.Rename(ctx, id, spec.Rel, spec.To)
	case gsExtract:
		err = host.Extract(ctx, id, spec.Rel, m.GetData(), filepath.Base(spec.Name))
	case gsBackup:
		var b gameserver.BackupFile
		b, err = host.Backup(ctx, id, spec.BackupID)
		res.Backup = &b
	case gsRestoreBackup:
		err = host.RestoreBackup(ctx, id, spec.BackupID)
	case gsDeleteBackup:
		err = host.DeleteBackup(ctx, id, spec.BackupID)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("game server action %q is not supported", action))
	}
	if err != nil {
		return nil, gameErr(err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: action, ResultJson: mustJSON(res)}), nil
}

func setGameProgress(id string, p gameProgress) {
	gameProgMu.Lock()
	gameProgMap[id] = p
	gameProgMu.Unlock()
}

func clearGameProgress(id string) {
	gameProgMu.Lock()
	delete(gameProgMap, id)
	gameProgMu.Unlock()
}
