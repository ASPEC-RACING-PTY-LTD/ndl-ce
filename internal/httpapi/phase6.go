package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
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
)

const (
	ioTicketHeader = "X-Nodal-Ticket"
	ioTicketTTL    = 2 * time.Minute
	vmUnsupported  = "No-dal Guest Agent required. VM Terminal and Files are introduced in a later platform phase."
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
	Path     string `json:"path"`
	DestPath string `json:"dest_path"`
	Mode     uint32 `json:"mode"`
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
	s.createTerminal(w, r, p, appdb.IOTargetHost, node.ID, "/")
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
	s.createTerminal(w, r, p, wl.Kind, wl.ID, jail)
}

func (s *Server) createTerminal(w http.ResponseWriter, r *http.Request, p *principal, targetKind, targetID, jail string) {
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
	s.audit(r, p.User.ClusterID, p.User.ID, "terminal.open", "ok", row.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": row.ID, "target_kind": row.TargetKind, "target_id": row.TargetID,
		"kind": row.Kind, "cwd": row.CWD, "state": row.State,
		"expires_at": row.ExpiresAt.UTC().Format(time.RFC3339),
		"ticket":     ticket, "jail_root": jail,
		"ws_path": "/api/v1/io/sessions/" + row.ID + "/ws",
	})
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
	switch action {
	case "download":
		rc, err := s.IO.FilesGet(r.Context(), agentrpc.FilesGetCall{
			TargetKind: kind, TargetID: id, JailRoot: jail, Path: rel,
		})
		if err != nil {
			s.audit(r, p.User.ClusterID, p.User.ID, "files.download", "denied", err.Error())
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		defer rc.Close()
		s.audit(r, p.User.ClusterID, p.User.ID, "files.download", "ok", rel)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+attachmentName(rel)+`"`)
		_, _ = io.Copy(w, rc)
	case "upload":
		upPath, body, closer, sha, err := readFileUpload(r)
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
		raw, err := s.IO.FilesPut(r.Context(), agentrpc.FilesPutCall{
			TargetKind: kind, TargetID: id, JailRoot: jail, Path: rel,
		}, body, sha)
		if err != nil {
			s.audit(r, p.User.ClusterID, p.User.ID, "files.upload", "denied", err.Error())
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.audit(r, p.User.ClusterID, p.User.ID, "files.upload", "ok", rel)
		writeJSON(w, http.StatusCreated, json.RawMessage(raw))
	case "list", "stat", "mkdir", "delete", "rename":
		mut := fileMutation{Path: rel}
		if action == "mkdir" || action == "delete" || action == "rename" {
			if r.Body != nil && r.ContentLength != 0 {
				_ = readJSON(r, &mut)
			}
		}
		if mut.Path == "" {
			mut.Path = rel
		}
		raw, err := s.IO.FilesOp(r.Context(), agentrpc.FilesCall{
			TargetKind: kind, TargetID: id, JailRoot: jail,
			Action: action, Path: mut.Path, DestPath: mut.DestPath, Mode: mut.Mode,
		})
		if err != nil {
			if action == "delete" {
				s.audit(r, p.User.ClusterID, p.User.ID, "files.delete", "denied", err.Error())
			}
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		if action == "delete" {
			s.audit(r, p.User.ClusterID, p.User.ID, "files.delete", "ok", mut.Path)
		}
		if action == "mkdir" {
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
	if row.Kind != lxc.KindSystemContainer {
		return *row, "", statusError{status: http.StatusUnprocessableEntity, msg: vmUnsupported}
	}
	jail, err := s.workloadJail(ctx, clusterID, *row)
	if err != nil {
		return *row, "", err
	}
	return *row, jail, nil
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
		return (&qemu.Engine{}).ConsoleSocket(row.TargetID, row.CWD)
	}
	wl, err := s.Store.GetWorkload(ctx, clusterID, row.TargetID)
	if err != nil || wl == nil {
		return "", errNotFound("workload not found")
	}
	return s.workloadJail(ctx, clusterID, *wl)
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

func readFileUpload(r *http.Request) (rel string, body io.Reader, closer func(), sha string, err error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return "", nil, nil, "", err
		}
		rel = strings.TrimSpace(r.FormValue("path"))
		sha = strings.TrimSpace(r.FormValue("sha256"))
		f, hdr, err := r.FormFile("file")
		if err != nil {
			return "", nil, nil, "", err
		}
		if rel == "" && hdr != nil {
			rel = hdr.Filename
		}
		return rel, f, func() { _ = f.Close() }, sha, nil
	}
	if strings.Contains(ct, "application/json") {
		return "", nil, nil, "", errors.New("upload must be multipart or a raw body")
	}
	return strings.TrimSpace(r.URL.Query().Get("path")), r.Body, func() { _ = r.Body.Close() }, "", nil
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
