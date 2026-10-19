package httpapi

import (
	"net/http"

	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

const hostTunnelCode = "host_tunnel_protected"

// hostTunnelGate guards changes to the host's own cloudflared, which usually
// carries the panel itself. No-dal leaves it alone: a request touching it is
// refused with advice, and goes ahead only when a SuperAdmin sent approve_host
// after seeing that advice. It reports whether the change was approved and
// whether the caller may continue.
func (s *Server) hostTunnelGate(w http.ResponseWriter, r *http.Request, p *principal, machineID, unit string, approve bool) (approved, ok bool) {
	if !docker.IsHostMachine(machineID) {
		return false, true
	}
	owner := hasRole(p, rbac.SuperAdmin)
	body := map[string]any{
		"code":        hostTunnelCode,
		"advice":      docker.HostTunnelAdvice(unit),
		"can_approve": owner,
	}
	if !approve {
		body["error"] = "This is the host's own cloudflared tunnel, which No-dal does not manage."
		writeJSON(w, http.StatusConflict, body)
		return false, false
	}
	if !owner {
		body["error"] = "Only a SuperAdmin can approve changes to the host's cloudflared tunnel."
		writeJSON(w, http.StatusForbidden, body)
		return false, false
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "cloudflare.host_tunnel.approved", "ok", unit)
	return true, true
}
