package httpapi

import (
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/guest"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/vmspec"
)

func (s *Server) getWorkloadGuest(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	if wl.Kind == lxc.KindSystemContainer {
		writeErr(w, http.StatusUnprocessableEntity, "guest agent applies to VMs")
		return
	}
	if wl.Kind != vmspec.KindVM {
		writeErr(w, http.StatusUnprocessableEntity, "guest agent applies to VMs")
		return
	}
	st := guest.Status{
		WorkloadID: wl.ID,
		QEMUGA:     guest.ChannelState{State: guest.StateUnavailable, Reason: "vm agent is unavailable"},
		NodalGA:    guest.ChannelState{State: guest.StateUnavailable, Reason: "vm agent is unavailable"},
		ObservedAt: s.now(),
	}
	if s.VM != nil {
		got, gerr := s.VM.GuestStatus(r.Context(), wl.ID)
		if gerr != nil {
			st.QEMUGA.Reason = gerr.Error()
			st.NodalGA.Reason = gerr.Error()
		} else {
			st = got
			if st.WorkloadID == "" {
				st.WorkloadID = wl.ID
			}
			if st.ObservedAt.IsZero() {
				st.ObservedAt = s.now()
			}
		}
	}
	_ = s.Store.UpsertGuestObservation(r.Context(), appdb.GuestObservation{
		WorkloadID:     wl.ID,
		ClusterID:      p.User.ClusterID,
		QEMUGAState:    st.QEMUGA.State,
		NodalGAState:   st.NodalGA.State,
		NodalGAVersion: st.NodalGA.Version,
		GuestOS:        st.GuestOS,
		GuestArch:      st.GuestArch,
		GuestIPv4:      strings.Join(st.IPv4, ","),
		ObservedAt:     st.ObservedAt,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"workload_id": st.WorkloadID,
		"qemu_ga":     st.QEMUGA,
		"nodal_ga":    st.NodalGA,
		"guest_os":    emptyAsNil(st.GuestOS),
		"guest_arch":  emptyAsNil(st.GuestArch),
		"ipv4":        st.IPv4,
		"observed_at": st.ObservedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		"install": map[string]any{
			"linux":   "Install the ndl-guest package inside the guest and enable ndl-guest.service. The virtio-serial channel org.nodal.guest.0 is attached to every No-dal VM.",
			"windows": "Install ndl-guest.exe inside the guest. Windows currently exposes shutdown, IP, and Files. PTY stays on Console until the Windows subset grows.",
		},
	})
}

func emptyAsNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
