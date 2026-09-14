package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/guest"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func guestIOReason(st guest.Status, err error) (string, int) {
	if err != nil {
		msg := strings.TrimSpace(err.Error())
		if msg == "" {
			msg = "vm agent is unavailable"
		}
		return msg, http.StatusBadGateway
	}
	state := strings.TrimSpace(st.NodalGA.State)
	reason := strings.TrimSpace(st.NodalGA.Reason)
	if state == guest.StateOK {
		return "", 0
	}
	if reason == "" {
		if state == "" {
			state = guest.StateUnavailable
		}
		reason = "No-dal Guest Agent is " + state
	}
	return reason, http.StatusUnprocessableEntity
}

func (s *Server) requireVMGuestJail(ctx context.Context, workloadID string) (string, error) {
	if s.VM == nil {
		return "", statusError{status: http.StatusBadGateway, msg: "vm agent is unavailable"}
	}
	st, err := s.VM.GuestStatus(ctx, workloadID)
	reason, code := guestIOReason(st, err)
	if code != 0 {
		return "", statusError{status: code, msg: reason}
	}
	return guest.JailRoot, nil
}

func auditFilesPath(kind, rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." {
		rel = "/"
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	if kind == vmspec.KindVM || kind == "vm-guest" {
		return "vm:" + rel
	}
	if kind == lxc.KindSystemContainer {
		return "ct:" + rel
	}
	return rel
}
