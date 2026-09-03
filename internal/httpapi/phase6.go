package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/iojail"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/secutil"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

const (
	ioTicketHeader = "X-Nodal-Ticket"
	ioTicketTTL    = 2 * time.Minute
	vmUnsupported  = "No-dal Guest Agent is not connected"
)

// IORPC is the privileged agent surface for Files and Terminal.
type IORPC interface {
	FilesOp(ctx context.Context, call agentrpc.FilesCall) (json.RawMessage, error)
	FilesPut(ctx context.Context, call agentrpc.FilesPutCall, r io.Reader, expectedSHA string) (json.RawMessage, error)
	FilesGet(ctx context.Context, call agentrpc.FilesGetCall) (io.ReadCloser, error)
	OpenTerminal(ctx context.Context, open agentrpc.TermOpen) (agentrpc.TermConn, error)
}

type createTermRequest struct {
	CWD  string `json:"cwd"`
	Mode string `json:"mode"`
}

type fileMutation struct {
	Path           string  `json:"path"`
	DestPath       string  `json:"dest_path"`
	Mode           *uint32 `json:"mode"`
	UID            *int    `json:"uid"`
	GID            *int    `json:"gid"`
	ExpectedMtime  string  `json:"expected_mtime"`
	ExpectedSHA256 string  `json:"expected_sha256"`
}

func (s *Server) createNodeTerminal(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.TerminalOpen)
	if err != nil {
		return
	}
	if !hasRole(p, rbac.Admin) {
		s.audit(r, p.User.ClusterID, p.User.ID, "terminal.open", "denied", "host terminal is admin-only")
		writeErr(w, http.StatusForbidden, "host terminal requires admin")
		return
	}
	node, err := s.localNode(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.createTerminal(w, r, p, appdb.IOTargetHost, node.ID, "/", node.ID)
}

func (s *Server) createWorkloadTerminal(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.TerminalOpen)
	if err != nil {
		return
	}
	wl, jail, err := s.workloadIO(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	nodeID := wl.NodeID
	if nodeID == "" {
		nodeID = wl.OwnerNodeID
	}
	s.createTerminal(w, r, p, wl.Kind, wl.ID, jail, nodeID)
}

func (s *Server) createTerminal(w http.ResponseWriter, r *http.Request, p *principal, targetKind, targetID, jail, nodeID string) {
	var req createTermRequest
	if r.ContentLength > 0 {
		_ = readJSON(r, &req)
	}
	kind := appdb.IOKindTerminal
	if strings.EqualFold(strings.TrimSpace(req.Mode), "console") {
		kind = appdb.IOKindConsole
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = "/"
	}
	ticket, err := secutil.RandomHex(24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	row := appdb.IOSession{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, UserID: p.User.ID,
		TargetKind: targetKind, TargetID: targetID, Kind: kind, CWD: cwd,
		TicketHash: secutil.HashSHA256(ticket), State: appdb.IOStatePending,
		ExpiresAt: s.now().Add(ioTicketTTL),
	}
	if err := s.Store.CreateIOSession(r.Context(), row); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "terminal.open", "ok", auditFilesPath(targetKind, cwd)+" "+row.ID)
	out := map[string]any{
		"id": row.ID, "target_kind": row.TargetKind, "target_id": row.TargetID,
		"kind": row.Kind, "cwd": row.CWD, "state": row.State,
		"expires_at": row.ExpiresAt.UTC().Format(time.RFC3339),
		"ticket":     ticket, "jail_root": jail,
		"ws_path": "/api/v1/io/sessions/" + row.ID + "/ws",
	}
	if nodeID != "" {
		out["node_id"] = nodeID
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) listIOSessions(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.TerminalOpen)
	if err != nil {
		return
	}
	rows, err := s.Store.ListIOSessions(r.Context(), p.User.ClusterID, p.User.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row.Kind != appdb.IOKindTerminal {
			continue
		}
		items = append(items, ioSessionJSON(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getIOSession(w http.ResponseWriter, r *http.Request) {
	p, err := s.principal(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	row, err := s.Store.GetIOSession(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	need := rbac.TerminalOpen
	if row.Kind == appdb.IOKindConsole && row.TargetKind == appdb.IOTargetVM {
		need = rbac.ComputeConsole
	}
	if !rbac.Authorize(p.Grants, need) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	if row.UserID != p.User.ID && !hasRole(p, rbac.Admin) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	writeJSON(w, http.StatusOK, ioSessionJSON(*row))
}

func (s *Server) ioSessionWS(w http.ResponseWriter, r *http.Request) {
	if !s.requireTLS(w, r) {
		return
	}
	if r.URL.Query().Get("ticket") != "" || r.URL.Query().Get("X-Nodal-Ticket") != "" {
		writeErr(w, http.StatusBadRequest, "ticket must be sent in X-Nodal-Ticket")
		return
	}
	if !originOK(r) {
		writeErr(w, http.StatusForbidden, "origin is not allowed")
		return
	}
	p, err := s.principal(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	ticket := wsTicket(r)
	if ticket == "" {
		writeErr(w, http.StatusUnauthorized, "X-Nodal-Ticket is required")
		return
	}
	row, err := s.Store.GetIOSessionByTicketHash(r.Context(), secutil.HashSHA256(ticket))
	if err != nil || row == nil || row.ID != r.PathValue("id") || row.ClusterID != p.User.ClusterID {
		writeErr(w, http.StatusUnauthorized, "invalid ticket")
		return
	}
	need := rbac.TerminalOpen
	if row.Kind == appdb.IOKindConsole && row.TargetKind == appdb.IOTargetVM {
		need = rbac.ComputeConsole
	}
	if !rbac.Authorize(p.Grants, need) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	if row.UserID != p.User.ID && !hasRole(p, rbac.Admin) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	if s.now().After(row.ExpiresAt) && (row.State == appdb.IOStatePending || row.Kind == appdb.IOKindConsole) {
		row.State = appdb.IOStateExpired
		row.Reason = "ticket expired"
		_ = s.Store.UpdateIOSession(r.Context(), *row)
		writeErr(w, http.StatusGone, "ticket expired")
		return
	}
	if row.State != appdb.IOStatePending && row.State != appdb.IOStateConnected {
		writeErr(w, http.StatusConflict, "session is not connectable")
		return
	}
	if s.IO == nil {
		writeErr(w, http.StatusBadGateway, "io agent is unavailable")
		return
	}
	jail, err := s.sessionJail(r.Context(), p.User.ClusterID, *row)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	cwd := row.CWD
	if row.Kind == appdb.IOKindConsole && row.TargetKind != appdb.IOTargetVM {
		cwd = "console"
	}
	conn, err := s.IO.OpenTerminal(r.Context(), agentrpc.TermOpen{
		TargetKind: row.TargetKind, TargetID: row.TargetID, JailRoot: jail, CWD: cwd,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer conn.Close()
	upgrader := websocket.Upgrader{
		CheckOrigin:  func(req *http.Request) bool { return originOK(req) },
		Subprotocols: wsTicketProtocols(r),
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	now := s.now()
	row.State = appdb.IOStateConnected
	row.ConnectedAt = &now
	_ = s.Store.UpdateIOSession(r.Context(), *row)
	errCh := make(chan error, 2)
	go func() {
		for {
			_, data, rerr := ws.ReadMessage()
			if rerr != nil {
				errCh <- rerr
				return
			}
			if err := conn.Send(data); err != nil {
				errCh <- err
				return
			}
		}
	}()
	go func() {
		for {
			data, rerr := conn.Recv()
			if rerr != nil {
				errCh <- rerr
				return
			}
			if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
				errCh <- err
				return
			}
		}
	}()
	<-errCh
	ended := s.now()
	row.State = appdb.IOStateEnded
	row.EndedAt = &ended
	row.Reason = "session ended"
	_ = s.Store.UpdateIOSession(context.Background(), *row)
}

func (s *Server) nodeFilesList(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesRead, "list")
}
func (s *Server) nodeFilesStat(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesRead, "stat")
}
func (s *Server) nodeFilesDownload(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesDownload, "download")
}
func (s *Server) nodeFilesUpload(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesUpload, "upload")
}
func (s *Server) nodeFilesMkdir(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesCreate, "mkdir")
}
func (s *Server) nodeFilesDelete(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesDelete, "delete")
}
func (s *Server) nodeFilesMove(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesModify, "rename")
}
func (s *Server) nodeFilesCopy(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesCreate, "copy")
}
func (s *Server) nodeFilesChmod(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesPermissions, "chmod")
}
func (s *Server) nodeFilesChown(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesOwnership, "chown")
}
func (s *Server) nodeFilesContent(w http.ResponseWriter, r *http.Request) {
	s.nodeFiles(w, r, rbac.FilesRead, "content")
}

func (s *Server) workloadFilesList(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesRead, "list")
}
func (s *Server) workloadFilesStat(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesRead, "stat")
}
func (s *Server) workloadFilesDownload(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesDownload, "download")
}
func (s *Server) workloadFilesUpload(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesUpload, "upload")
}
func (s *Server) workloadFilesMkdir(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesCreate, "mkdir")
}
func (s *Server) workloadFilesDelete(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesDelete, "delete")
}
func (s *Server) workloadFilesMove(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesModify, "rename")
}
func (s *Server) workloadFilesCopy(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesCreate, "copy")
}
func (s *Server) workloadFilesChmod(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesPermissions, "chmod")
}
func (s *Server) workloadFilesChown(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesOwnership, "chown")
}
func (s *Server) workloadFilesContent(w http.ResponseWriter, r *http.Request) {
	s.workloadFiles(w, r, rbac.FilesRead, "content")
}

func (s *Server) nodeFiles(w http.ResponseWriter, r *http.Request, perm, action string) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return
	}
	if !hasRole(p, rbac.Admin) {
		s.audit(r, p.User.ClusterID, p.User.ID, "files."+action, "denied", "host files are admin-only")
		writeErr(w, http.StatusForbidden, "host files require admin")
		return
	}
	node, err := s.localNode(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.handleFiles(w, r, p, iojail.TargetHost, node.ID, "/", action)
}

func (s *Server) workloadFiles(w http.ResponseWriter, r *http.Request, perm, action string) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return
	}
	wl, jail, err := s.workloadIO(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	s.handleFiles(w, r, p, wl.Kind, wl.ID, jail, action)
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request, p *principal, kind, id, jail, action string) {
	if s.IO == nil {
		writeErr(w, http.StatusBadGateway, "io agent is unavailable")
		return
	}
	rel := filePath(r)
	if _, err := iojail.CleanRel(rel); err != nil {
		writeErr(w, filesHTTPStatus(err), err.Error())
		return
	}
	switch action {
	case "download":
		rc, err := s.IO.FilesGet(r.Context(), agentrpc.FilesGetCall{
			TargetKind: kind, TargetID: id, JailRoot: jail, Path: rel,
		})
		if err != nil {
			s.audit(r, p.User.ClusterID, p.User.ID, "files.download", "denied", auditFilesPath(kind, rel))
			writeErr(w, filesHTTPStatus(err), err.Error())
			return
		}
		defer rc.Close()
		s.audit(r, p.User.ClusterID, p.User.ID, "files.download", "ok", auditFilesPath(kind, rel))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+attachmentName(rel)+`"`)
		_, _ = io.Copy(w, rc)
	case "upload":
		upPath, body, closer, contentSHA, casSHA, expectedMtime, err := readFileUpload(r)
		if closer != nil {
			defer closer()
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if upPath != "" {
			rel = upPath
		}
		if _, err := iojail.CleanRel(rel); err != nil {
			writeErr(w, filesHTTPStatus(err), err.Error())
			return
		}
		if err := s.enforceExpected(r.Context(), kind, id, jail, rel, expectedMtime, casSHA); err != nil {
			writeErr(w, filesHTTPStatus(err), err.Error())
			return
		}
		raw, err := s.IO.FilesPut(r.Context(), agentrpc.FilesPutCall{
			TargetKind: kind, TargetID: id, JailRoot: jail, Path: rel,
		}, body, contentSHA)
		if err != nil {
			s.audit(r, p.User.ClusterID, p.User.ID, "files.upload", "denied", auditFilesPath(kind, rel))
			writeErr(w, filesHTTPStatus(err), err.Error())
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "files.upload", "ok", auditFilesPath(kind, rel))
		writeJSON(w, http.StatusCreated, json.RawMessage(raw))
	case "content":
		s.serveFileContent(w, r, p, kind, id, jail, rel)
	case "list", "stat", "mkdir", "delete", "rename", "copy", "chmod", "chown":
		mut := fileMutation{Path: rel}
		if action != "list" && action != "stat" {
			if r.Body != nil && r.ContentLength != 0 {
				_ = readJSON(r, &mut)
			}
		}
		if mut.Path == "" {
			mut.Path = rel
		}
		if _, err := iojail.CleanRel(mut.Path); err != nil {
			writeErr(w, filesHTTPStatus(err), err.Error())
			return
		}
		if err := s.enforceExpected(r.Context(), kind, id, jail, mut.Path, mut.ExpectedMtime, mut.ExpectedSHA256); err != nil {
			writeErr(w, filesHTTPStatus(err), err.Error())
			return
		}
		dest := mut.DestPath
		if action == "rename" || action == "copy" {
			if strings.TrimSpace(dest) == "" {
				writeErr(w, http.StatusBadRequest, "dest_path is required")
				return
			}
			if _, err := iojail.CleanRel(dest); err != nil {
				writeErr(w, filesHTTPStatus(err), err.Error())
				return
			}
		}
		if action == "chown" {
			uid, gid := -1, -1
			if mut.UID != nil {
				uid = *mut.UID
			}
			if mut.GID != nil {
				gid = *mut.GID
			}
			if dest == "" {
				dest = fmt.Sprintf("%d:%d", uid, gid)
			}
		}
		if action == "chmod" && mut.Mode == nil {
			writeErr(w, http.StatusBadRequest, "mode is required")
			return
		}
		mode := uint32(0)
		if mut.Mode != nil {
			mode = *mut.Mode
		}
		raw, err := s.IO.FilesOp(r.Context(), agentrpc.FilesCall{
			TargetKind: kind, TargetID: id, JailRoot: jail,
			Action: action, Path: mut.Path, DestPath: dest, Mode: mode,
		})
		if err != nil {
			if action == "delete" {
				s.audit(r, p.User.ClusterID, p.User.ID, "files.delete", "denied", auditFilesPath(kind, mut.Path))
			}
			writeErr(w, filesHTTPStatus(err), err.Error())
			return
		}
		if action == "delete" {
			s.audit(r, p.User.ClusterID, p.User.ID, "files.delete", "ok", auditFilesPath(kind, mut.Path))
		}
		if action == "mkdir" || action == "copy" {
			writeJSON(w, http.StatusCreated, json.RawMessage(raw))
			return
		}
		writeJSON(w, http.StatusOK, json.RawMessage(raw))
	default:
		writeErr(w, http.StatusBadRequest, "unknown files action")
	}
}

func (s *Server) localNode(ctx context.Context, clusterID, id string) (*appdb.Node, error) {
	node, err := s.Store.GetNode(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if node == nil || (id != "" && node.ID != id) {
		return nil, errNotFound("node not found")
	}
	return node, nil
}

func (s *Server) workloadIO(ctx context.Context, clusterID, id string) (appdb.Workload, string, error) {
	row, err := s.Store.GetWorkload(ctx, clusterID, id)
	if err != nil || row == nil {
		return appdb.Workload{}, "", errNotFound("workload not found")
	}
	switch row.Kind {
	case lxc.KindSystemContainer:
		jail, err := s.workloadJail(ctx, clusterID, *row)
		if err != nil {
			return *row, "", err
		}
		return *row, jail, nil
	case vmspec.KindVM:
		jail, err := s.requireVMGuestJail(ctx, row.ID)
		if err != nil {
			return *row, "", err
		}
		return *row, jail, nil
	default:
		return *row, "", statusError{status: http.StatusUnprocessableEntity, msg: vmUnsupported}
	}
}

func (s *Server) workloadJail(ctx context.Context, clusterID string, w appdb.Workload) (string, error) {
	disks, err := s.Store.ListWorkloadDisks(ctx, clusterID, w.ID)
	if err != nil || len(disks) == 0 {
		return "", errConflict("workload has no root volume")
	}
	vol, err := s.Store.GetVolume(ctx, clusterID, disks[0].VolumeID)
	if err != nil || vol == nil {
		return "", errConflict("workload volume is unavailable")
	}
	pool, err := s.Store.GetStoragePool(ctx, clusterID, vol.PoolID)
	if err != nil || pool == nil {
		return "", errConflict("workload pool is unavailable")
	}
	joined, err := storage.JoinUnder(pool.RootPath, vol.BackendRef)
	if err != nil {
		return "", errConflict("workload volume locator is invalid")
	}
	return joined, nil
}

func (s *Server) sessionJail(ctx context.Context, clusterID string, row appdb.IOSession) (string, error) {
	if row.TargetKind == appdb.IOTargetHost {
		return "/", nil
	}
	if row.TargetKind == appdb.IOTargetVM {
		if row.Kind == appdb.IOKindConsole {
			return (&qemu.Engine{}).ConsoleSocket(row.TargetID, row.CWD)
		}
		return s.requireVMGuestJail(ctx, row.TargetID)
	}
	wl, err := s.Store.GetWorkload(ctx, clusterID, row.TargetID)
	if err != nil || wl == nil {
		return "", errNotFound("workload not found")
	}
	return s.workloadJail(ctx, clusterID, *wl)
}

func (s *Server) serveFileContent(w http.ResponseWriter, r *http.Request, p *principal, kind, id, jail, rel string) {
	raw, err := s.IO.FilesOp(r.Context(), agentrpc.FilesCall{
		TargetKind: kind, TargetID: id, JailRoot: jail, Action: "stat", Path: rel,
	})
	if err != nil {
		writeErr(w, filesHTTPStatus(err), err.Error())
		return
	}
	var st struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Size    int64  `json:"size"`
		Mode    uint32 `json:"mode"`
		UID     uint32 `json:"uid"`
		GID     uint32 `json:"gid"`
		Owner   string `json:"owner"`
		Group   string `json:"group"`
		ModTime string `json:"mtime"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		writeErr(w, http.StatusBadGateway, "stat is unreadable")
		return
	}
	if st.Type == "dir" {
		writeErr(w, http.StatusBadRequest, "path is a directory")
		return
	}
	max := int64(iojail.PreviewMaxBytes)
	if n, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("max_bytes")), 10, 64); err == nil && n > 0 && n < max {
		max = n
	}
	out := map[string]any{
		"name": st.Name, "type": st.Type, "size": st.Size, "mode": st.Mode,
		"uid": st.UID, "gid": st.GID, "owner": st.Owner, "group": st.Group,
		"mtime": st.ModTime, "path": st.Path, "editable": false, "binary": false,
		"too_large": false,
	}
	if st.Size > iojail.PreviewMaxBytes {
		out["too_large"] = true
		writeJSON(w, http.StatusOK, out)
		return
	}
	rc, err := s.IO.FilesGet(r.Context(), agentrpc.FilesGetCall{
		TargetKind: kind, TargetID: id, JailRoot: jail, Path: rel,
	})
	if err != nil {
		writeErr(w, filesHTTPStatus(err), err.Error())
		return
	}
	defer rc.Close()
	limited := io.LimitReader(rc, max+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		writeErr(w, filesHTTPStatus(err), err.Error())
		return
	}
	if int64(len(body)) > max {
		out["too_large"] = true
		writeJSON(w, http.StatusOK, out)
		return
	}
	sum := sha256.Sum256(body)
	out["sha256"] = hex.EncodeToString(sum[:])
	if iojail.LooksBinary(st.Name, body) {
		out["binary"] = true
		s.audit(r, p.User.ClusterID, p.User.ID, "files.read", "ok", auditFilesPath(kind, rel))
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["encoding"] = "utf-8"
	out["content"] = string(body)
	out["editable"] = st.Size <= iojail.EditorMaxBytes && int64(len(body)) <= iojail.EditorMaxBytes
	s.audit(r, p.User.ClusterID, p.User.ID, "files.read", "ok", auditFilesPath(kind, rel))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) enforceExpected(ctx context.Context, kind, id, jail, rel, expectedMtime, expectedSHA string) error {
	expectedMtime = strings.TrimSpace(expectedMtime)
	expectedSHA = strings.TrimSpace(expectedSHA)
	if expectedMtime == "" && expectedSHA == "" {
		return nil
	}
	raw, err := s.IO.FilesOp(ctx, agentrpc.FilesCall{
		TargetKind: kind, TargetID: id, JailRoot: jail, Action: "stat", Path: rel,
	})
	if err != nil {
		return fmt.Errorf("file changed on disk: %w", err)
	}
	var st struct {
		ModTime string `json:"mtime"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("file changed on disk")
	}
	if expectedMtime != "" && st.ModTime != "" && st.ModTime != expectedMtime {
		return fmt.Errorf("file changed on disk")
	}
	if expectedSHA == "" || st.Type == "dir" {
		return nil
	}
	rc, err := s.IO.FilesGet(ctx, agentrpc.FilesGetCall{
		TargetKind: kind, TargetID: id, JailRoot: jail, Path: rel,
	})
	if err != nil {
		return fmt.Errorf("file changed on disk: %w", err)
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return err
	}
	if !strings.EqualFold(expectedSHA, hex.EncodeToString(h.Sum(nil))) {
		return fmt.Errorf("file changed on disk")
	}
	return nil
}

func filesHTTPStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "file changed"):
		return http.StatusConflict
	case strings.Contains(msg, "sha256 mismatch"):
		return http.StatusBadRequest
	case strings.Contains(msg, "escapes"), strings.Contains(msg, "denied by host"):
		return http.StatusForbidden
	case strings.Contains(msg, "no such file"), strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case strings.Contains(msg, "permission denied"):
		return http.StatusForbidden
	default:
		return http.StatusBadGateway
	}
}

func attachmentName(rel string) string {
	name := path.Base(strings.ReplaceAll(rel, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r == '"' || r == '\n' || r == '\r' || r == '\x00' {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		return "download"
	}
	return name
}

func filePath(r *http.Request) string {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		return "."
	}
	return p
}

func readFileUpload(r *http.Request) (rel string, body io.Reader, closer func(), contentSHA, casSHA, expectedMtime string, err error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return "", nil, nil, "", "", "", err
		}
		rel = strings.TrimSpace(r.FormValue("path"))
		contentSHA = strings.TrimSpace(r.FormValue("sha256"))
		casSHA = strings.TrimSpace(r.FormValue("expected_sha256"))
		expectedMtime = strings.TrimSpace(r.FormValue("expected_mtime"))
		f, hdr, err := r.FormFile("file")
		if err != nil {
			return "", nil, nil, "", "", "", err
		}
		if rel == "" && hdr != nil {
			rel = hdr.Filename
		}
		return rel, f, func() { _ = f.Close() }, contentSHA, casSHA, expectedMtime, nil
	}
	if strings.Contains(ct, "application/json") {
		return "", nil, nil, "", "", "", errors.New("upload must be multipart or a raw body")
	}
	return strings.TrimSpace(r.URL.Query().Get("path")), r.Body, func() { _ = r.Body.Close() }, "", "", strings.TrimSpace(r.Header.Get("X-Nodal-Expected-Mtime")), nil
}

func wsTicket(r *http.Request) string {
	if t := strings.TrimSpace(r.Header.Get(ioTicketHeader)); t != "" {
		return t
	}
	for _, p := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "ndl.ticket.") {
			return strings.TrimPrefix(p, "ndl.ticket.")
		}
	}
	return ""
}

func wsTicketProtocols(r *http.Request) []string {
	var out []string
	for _, p := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func originOK(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func ioSessionJSON(row appdb.IOSession) map[string]any {
	out := map[string]any{
		"id": row.ID, "target_kind": row.TargetKind, "target_id": row.TargetID,
		"kind": row.Kind, "cwd": row.CWD, "state": row.State, "reason": row.Reason,
		"expires_at": row.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if row.ConnectedAt != nil {
		out["connected_at"] = row.ConnectedAt.UTC().Format(time.RFC3339)
	}
	if row.EndedAt != nil {
		out["ended_at"] = row.EndedAt.UTC().Format(time.RFC3339)
	}
	return out
}
