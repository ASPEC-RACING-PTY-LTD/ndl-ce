package agentrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

// GameHost is the control plane's gameserver.Host: every operation runs in
// the root agent, so the unprivileged control plane never touches Docker or
// the server folders itself.
type GameHost struct {
	Client Client
	// ProgressEvery is how often a running install is polled for progress.
	ProgressEvery time.Duration
}

var _ gameserver.Host = GameHost{}

func (g GameHost) call(ctx context.Context, action, id string, spec gameSpec, data []byte) (gameResult, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return gameResult{}, err
	}
	res, err := g.Client.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_GameServer{GameServer: &agentv1.GameServer{
			Action: action, ServerId: id, SpecJson: raw, Data: data,
		}},
	}))
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			if ce.Code() == connect.CodeNotFound {
				return gameResult{}, fmt.Errorf("%s: %w", ce.Message(), fs.ErrNotExist)
			}
			return gameResult{}, errors.New(ce.Message())
		}
		return gameResult{}, err
	}
	var out gameResult
	if len(res.Msg.GetResultJson()) > 0 {
		if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
			return gameResult{}, err
		}
	}
	return out, nil
}

// Ready reports whether the agent's host can run game servers.
func (g GameHost) Ready(ctx context.Context) error {
	_, err := g.call(ctx, gsReady, "", gameSpec{}, nil)
	return err
}

func (g GameHost) EnsureData(ctx context.Context, id string) (string, error) {
	res, err := g.call(ctx, gsEnsureData, id, gameSpec{}, nil)
	return res.Dir, err
}

func (g GameHost) RemoveData(ctx context.Context, id string) error {
	_, err := g.call(ctx, gsRemoveData, id, gameSpec{}, nil)
	return err
}

// Install runs the install in the agent and polls its progress while it
// runs, so the UI sees each phase as it happens.
func (g GameHost) Install(ctx context.Context, srv gameserver.Server, tmpl gameserver.Template, progress gameserver.ProgressFunc) error {
	done := make(chan struct{})
	if progress != nil {
		every := g.ProgressEvery
		if every <= 0 {
			every = 2 * time.Second
		}
		go func() {
			tick := time.NewTicker(every)
			defer tick.Stop()
			last := ""
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case <-tick.C:
					res, err := g.call(ctx, gsInstallProgress, srv.ID, gameSpec{}, nil)
					if err != nil || !res.Running {
						continue
					}
					if key := res.Phase + "\x00" + res.Text; key != last {
						last = key
						progress(res.Phase, res.Message, res.Text)
					}
				}
			}
		}()
	}
	_, err := g.call(ctx, gsInstall, srv.ID, gameSpec{Server: &srv, Template: &tmpl}, nil)
	close(done)
	return err
}

func (g GameHost) LogTail(ctx context.Context, id string, max int) string {
	res, _ := g.call(ctx, gsLogTail, id, gameSpec{Tail: max}, nil)
	return res.Text
}

func (g GameHost) Start(ctx context.Context, spec gameserver.RunSpec) (string, error) {
	res, err := g.call(ctx, gsStart, spec.ID, gameSpec{Run: &spec}, nil)
	return res.ID, err
}

func (g GameHost) Stop(ctx context.Context, id string) error {
	_, err := g.call(ctx, gsStop, id, gameSpec{}, nil)
	return err
}

func (g GameHost) StopGraceful(ctx context.Context, id, stopCmd string, timeout time.Duration) error {
	_, err := g.call(ctx, gsStopGraceful, id, gameSpec{StopCmd: stopCmd, TimeoutMS: timeout.Milliseconds()}, nil)
	return err
}

func (g GameHost) Kill(ctx context.Context, id string) error {
	_, err := g.call(ctx, gsKill, id, gameSpec{}, nil)
	return err
}

func (g GameHost) Running(ctx context.Context, id string) bool {
	res, err := g.call(ctx, gsRunning, id, gameSpec{}, nil)
	return err == nil && res.Running
}

func (g GameHost) Send(ctx context.Context, id, line string) error {
	_, err := g.call(ctx, gsSend, id, gameSpec{Line: line}, nil)
	return err
}

func (g GameHost) Logs(ctx context.Context, id string, tail int) (string, error) {
	res, err := g.call(ctx, gsLogs, id, gameSpec{Tail: tail}, nil)
	return res.Text, err
}

func (g GameHost) ListDir(ctx context.Context, id, rel string) (gameserver.DirListing, error) {
	res, err := g.call(ctx, gsListDir, id, gameSpec{Rel: rel}, nil)
	if err != nil {
		return gameserver.DirListing{}, err
	}
	if res.Listing == nil {
		return gameserver.DirListing{Path: rel, Items: []gameserver.DirEntry{}}, nil
	}
	return *res.Listing, nil
}

func (g GameHost) ReadFile(ctx context.Context, id, rel string, max int64) ([]byte, error) {
	res, err := g.call(ctx, gsReadFile, id, gameSpec{Rel: rel, Max: max}, nil)
	return res.Data, err
}

func (g GameHost) WriteFile(ctx context.Context, id, rel string, body []byte) error {
	_, err := g.call(ctx, gsWriteFile, id, gameSpec{Rel: rel}, body)
	return err
}

func (g GameHost) Mkdir(ctx context.Context, id, rel string) error {
	_, err := g.call(ctx, gsMkdir, id, gameSpec{Rel: rel}, nil)
	return err
}

func (g GameHost) Remove(ctx context.Context, id, rel string) error {
	_, err := g.call(ctx, gsRemove, id, gameSpec{Rel: rel}, nil)
	return err
}

func (g GameHost) Rename(ctx context.Context, id, from, to string) error {
	_, err := g.call(ctx, gsRename, id, gameSpec{Rel: from, To: to}, nil)
	return err
}

func (g GameHost) Extract(ctx context.Context, id, destRel string, raw []byte, name string) error {
	_, err := g.call(ctx, gsExtract, id, gameSpec{Rel: destRel, Name: name}, raw)
	return err
}

func (g GameHost) Backup(ctx context.Context, id, backupID string) (gameserver.BackupFile, error) {
	res, err := g.call(ctx, gsBackup, id, gameSpec{BackupID: backupID}, nil)
	if err != nil || res.Backup == nil {
		return gameserver.BackupFile{}, err
	}
	return *res.Backup, nil
}

func (g GameHost) RestoreBackup(ctx context.Context, id, backupID string) error {
	_, err := g.call(ctx, gsRestoreBackup, id, gameSpec{BackupID: backupID}, nil)
	return err
}

func (g GameHost) DeleteBackup(ctx context.Context, id, backupID string) error {
	_, err := g.call(ctx, gsDeleteBackup, id, gameSpec{BackupID: backupID}, nil)
	return err
}
