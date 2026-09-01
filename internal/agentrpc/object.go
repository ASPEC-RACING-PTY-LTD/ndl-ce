package agentrpc

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/objstore"
)

func (h *Handler) execBackupObject(ctx context.Context, m *agentv1.BackupObject) (*connect.Response[agentv1.ExecuteResponse], error) {
	req := objstore.Request{
		Action: m.GetAction(), Provider: m.GetProvider(), Endpoint: m.GetEndpoint(),
		Region: m.GetRegion(), Bucket: m.GetBucket(), Key: m.GetObjectKey(),
		SourcePath: m.GetSourcePath(), DestPath: m.GetDestPath(),
		AccessKeyID: m.GetAccessKeyId(), SecretAccessKey: m.GetSecretAccessKey(),
		EncryptionKey: m.GetEncryptionKey(), NoCheckBucket: m.GetNoCheckBucket(),
		ResumeUploadID: m.GetResumeUploadId(),
	}
	eng := &objstore.Engine{SkipNetwork: h.SkipHostCmds}
	if !h.SkipHostCmds {
		eng.Transport = objstore.NewS3Transport(req.Endpoint, req.Region, req.AccessKeyID, req.SecretAccessKey, req.Provider, nil)
	}
	res, err := eng.Do(ctx, req)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: m.GetAction(), ResultJson: mustJSON(res)}), nil
}

func (c Client) ObjectBackup(ctx context.Context, req objstore.Request) (objstore.Result, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_BackupObject{BackupObject: &agentv1.BackupObject{
			Action: req.Action, Provider: req.Provider, Endpoint: req.Endpoint, Region: req.Region,
			Bucket: req.Bucket, ObjectKey: req.Key, SourcePath: req.SourcePath, DestPath: req.DestPath,
			AccessKeyId: req.AccessKeyID, SecretAccessKey: req.SecretAccessKey,
			EncryptionKey: req.EncryptionKey, NoCheckBucket: req.NoCheckBucket,
			ResumeUploadId: req.ResumeUploadID,
		}},
	}))
	if err != nil {
		return objstore.Result{}, err
	}
	var out objstore.Result
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return objstore.Result{}, err
	}
	return out, nil
}
