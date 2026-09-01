package agentrpc

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func (h *Handler) driver() storage.Directory {
	if h.Storage != nil {
		return *h.Storage
	}
	return storage.Directory{}
}

func (h *Handler) uploads() *storage.Uploads {
	h.uploadOnce.Do(func() {
		if h.Uploads == nil {
			d := h.driver()
			h.Uploads = &storage.Uploads{Dir: d}
		}
	})
	return h.Uploads
}

func decodeHints(in []*agentv1.StoragePoolHint) []storage.PoolHint {
	var out []storage.PoolHint
	for _, h := range in {
		hint := storage.PoolHint{
			PoolID:      h.GetPoolId(),
			BackendType: h.GetBackendType(),
			RootPath:    h.GetRootPath(),
		}
		if len(h.GetBackingJson()) > 0 {
			_ = json.Unmarshal(h.GetBackingJson(), &hint.Backing)
		}
		out = append(out, hint)
	}
	return out
}

// GetStorage observes known Directory pools.
func (h *Handler) GetStorage(ctx context.Context, req *connect.Request[agentv1.GetStorageRequest]) (*connect.Response[agentv1.GetStorageResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	obs := h.driver().Observe(dirHints(decodeHints(req.Msg.GetStoragePools())))
	obs.Pools = append(obs.Pools, h.zfs().ObserveHints(ctx, zfsHints(decodeHints(req.Msg.GetStoragePools())))...)
	obs.Pools = append(obs.Pools, h.lvm().ObserveHints(ctx, lvmHints(decodeHints(req.Msg.GetStoragePools())))...)
	return connect.NewResponse(&agentv1.GetStorageResponse{StorageJson: mustJSON(obs)}), nil
}

func dirHints(in []storage.PoolHint) []storage.PoolHint {
	var out []storage.PoolHint
	for _, h := range in {
		if h.BackendType == storage.BackendZFS || h.BackendType == storage.BackendLVM {
			continue
		}
		out = append(out, h)
	}
	return out
}

func zfsHints(in []storage.PoolHint) []storage.PoolHint {
	var out []storage.PoolHint
	for _, h := range in {
		if h.BackendType == storage.BackendZFS {
			out = append(out, h)
		}
	}
	return out
}

func lvmHints(in []storage.PoolHint) []storage.PoolHint {
	var out []storage.PoolHint
	for _, h := range in {
		if h.BackendType == storage.BackendLVM {
			out = append(out, h)
		}
	}
	return out
}

func (h *Handler) observeStorage(hints []storage.PoolHint) []byte {
	var dir, zfs, lvm []storage.PoolHint
	for _, hint := range hints {
		switch hint.BackendType {
		case storage.BackendZFS:
			zfs = append(zfs, hint)
		case storage.BackendLVM:
			lvm = append(lvm, hint)
		default:
			dir = append(dir, hint)
		}
	}
	obs := h.driver().Observe(dir)
	obs.Pools = append(obs.Pools, h.zfs().ObserveHints(context.Background(), zfs)...)
	obs.Pools = append(obs.Pools, h.lvm().ObserveHints(context.Background(), lvm)...)
	return mustJSON(obs)
}

// UploadLibrary streams a library object onto an available Directory pool.
func (h *Handler) UploadLibrary(ctx context.Context, stream *connect.ClientStream[agentv1.UploadLibraryRequest]) (*connect.Response[agentv1.UploadLibraryResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	var itemID string
	var expected string
	started := false
	up := h.uploads()
	for stream.Receive() {
		msg := stream.Msg()
		switch p := msg.Payload.(type) {
		case *agentv1.UploadLibraryRequest_Begin:
			b := p.Begin
			itemID = b.GetItemId()
			hint := storage.PoolHint{PoolID: b.GetPoolId(), BackendType: storage.BackendDirectory, RootPath: b.GetRootPath()}
			if len(b.GetBackingJson()) > 0 {
				_ = json.Unmarshal(b.GetBackingJson(), &hint.Backing)
			}
			if err := up.Begin(hint, storage.BeginUploadRequest{
				ItemID: itemID, PoolID: b.GetPoolId(), Kind: b.GetKind(),
				DisplayName: b.GetDisplayName(), MaxBytes: b.GetMaxBytes(),
				RejectChecksums: b.GetRejectSha256(),
			}); err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, err)
			}
			started = true
		case *agentv1.UploadLibraryRequest_Chunk:
			if !started {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("upload begin is required"))
			}
			if err := up.Write(ctx, itemID, p.Chunk); err != nil {
				return nil, connect.NewError(connect.CodeFailedPrecondition, err)
			}
		case *agentv1.UploadLibraryRequest_Finish:
			expected = p.Finish.GetExpectedSha256()
		}
	}
	if err := stream.Err(); err != nil {
		if started {
			up.Abort(itemID)
		}
		return nil, err
	}
	if !started {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("upload begin is required"))
	}
	res, err := up.Finish(ctx, storage.FinishUploadRequest{ItemID: itemID, ExpectedSHA256: expected})
	if err != nil {
		up.Abort(itemID)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.UploadLibraryResponse{Ok: true, ResultJson: mustJSON(res)}), nil
}
