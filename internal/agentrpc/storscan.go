package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/storscan"
)

// StorageScanSpec is the request for a storage scan action.
type StorageScanSpec struct {
	Root    string   `json:"root,omitempty"`
	OneFS   bool     `json:"one_filesystem,omitempty"`
	ID      string   `json:"id,omitempty"`
	Path    string   `json:"path,omitempty"`
	Items   []string `json:"items,omitempty"`
	Protect []string `json:"protect,omitempty"`
	Keep    []string `json:"keep,omitempty"`
}

// StorageScanResult answers a storage scan action.
type StorageScanResult struct {
	Scan    storscan.Status         `json:"scan"`
	Node    *storscan.Node          `json:"node,omitempty"`
	Deleted []storscan.DeleteResult `json:"deleted,omitempty"`
}

var (
	scansOnce sync.Once
	scans     *storscan.Manager
)

func scanManager() *storscan.Manager {
	scansOnce.Do(func() { scans = &storscan.Manager{} })
	return scans
}

func (h *Handler) execStorageScan(_ context.Context, m *agentv1.StorageScan) (*connect.Response[agentv1.ExecuteResponse], error) {
	var spec StorageScanSpec
	if raw := m.GetSpecJson(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &spec); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("storage scan request is invalid"))
		}
	}
	mgr := scanManager()
	var out StorageScanResult
	switch m.GetAction() {
	case "scan-start":
		s, err := mgr.Start(spec.Root, spec.OneFS)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		out.Scan, _, _ = mgr.Get(s.ID)
	case "scan-status":
		st, tree, ok := mgr.Get(spec.ID)
		if !ok {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("the scan is gone; the agent may have restarted. Scan again"))
		}
		out.Scan = st
		if st.State != storscan.StateRunning && tree != nil {
			n, err := storscan.Find(tree, spec.Path)
			if err != nil {
				return nil, connect.NewError(connect.CodeNotFound, err)
			}
			sh := storscan.Shallow(n)
			out.Node = &sh
		}
	case "scan-cancel":
		mgr.Cancel(spec.ID)
		out.Scan, _, _ = mgr.Get(spec.ID)
	case "delete":
		st, _, ok := mgr.Get(spec.ID)
		if !ok || st.State == storscan.StateRunning {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("delete works on a finished scan"))
		}
		out.Scan = st
		out.Deleted = storscan.Delete(st.Root, spec.Items, spec.Protect, spec.Keep, nil)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown storage scan action %q", m.GetAction()))
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: m.GetAction(), ResultJson: mustJSON(out)}), nil
}

// StorageScan runs a storage scan action on the agent.
func (c Client) StorageScan(ctx context.Context, action string, spec StorageScanSpec) (StorageScanResult, error) {
	raw, _ := json.Marshal(spec)
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_StorageScan{StorageScan: &agentv1.StorageScan{Action: action, SpecJson: raw}},
	}))
	if err != nil {
		return StorageScanResult{}, err
	}
	var out StorageScanResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return StorageScanResult{}, err
	}
	return out, nil
}
