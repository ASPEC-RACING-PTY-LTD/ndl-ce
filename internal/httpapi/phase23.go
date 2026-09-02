package httpapi

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/objstore"
)

// ObjectRPC is the privileged agent surface for encrypt-before-upload object backups.
type ObjectRPC interface {
	ObjectBackup(ctx context.Context, req objstore.Request) (objstore.Result, error)
}

type objectUnavailable struct{}

func (objectUnavailable) ObjectBackup(context.Context, objstore.Request) (objstore.Result, error) {
	return objstore.Result{}, errUnavailable("object backup agent is unavailable")
}

func AdaptObject(client any) ObjectRPC {
	if v, ok := client.(ObjectRPC); ok {
		return v
	}
	return objectUnavailable{}
}

func (s *Server) objectRPC() ObjectRPC {
	if s.Object != nil {
		return s.Object
	}
	return AdaptObject(s.Agent)
}

func isObjectBackupKind(kind string) bool {
	return objstore.IsObjectKind(kind)
}

func (s *Server) probeObjectTarget(ctx context.Context, t appdb.BackupTarget) string {
	if t.NoCheckBucket {
		return appdb.BackupNotConfigured
	}
	pass, enc, err := s.Store.BackupCredentials(ctx, t.ClusterID, t.ID)
	if err != nil {
		return appdb.BackupUnavailable
	}
	key, err := objstore.ParseKey(enc)
	if err != nil {
		return appdb.BackupUnavailable
	}
	res, err := s.objectRPC().ObjectBackup(ctx, objstore.Request{
		Action: objstore.ActionHead, Provider: t.Kind, Endpoint: t.Endpoint, Region: t.Region,
		Bucket: t.Bucket, Key: objstore.ObjectKey(t.Prefix, "probe", "qcow2"),
		AccessKeyID: t.Username, SecretAccessKey: pass, EncryptionKey: key,
	})
	if err != nil {
		return appdb.BackupUnavailable
	}
	if res.Status == appdb.BackupAvailable || res.Status == appdb.BackupNotConfigured {
		return res.Status
	}
	if res.Status == "" {
		return appdb.BackupUnavailable
	}
	return res.Status
}

func validateObjectTarget(kind, endpoint, bucket string) error {
	if !isObjectBackupKind(kind) {
		return errBadRequest("kind must be local, nfs, smb, s3, r2, aws, b2, or minio")
	}
	if strings.TrimSpace(bucket) == "" {
		return errBadRequest("bucket is required")
	}
	if strings.Contains(bucket, "/") || strings.Contains(bucket, "..") {
		return errBadRequest("bucket is invalid")
	}
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errBadRequest("endpoint must be an http(s) URL")
	}
	if kind != appdb.BackupMinIO && u.Scheme != "https" {
		return errBadRequest("https endpoint is required except for minio test fixtures")
	}
	return nil
}

func backupTargetAllowsRun(t appdb.BackupTarget) bool {
	if t.Status == appdb.BackupAvailable {
		return true
	}
	return isObjectBackupKind(t.Kind) && t.NoCheckBucket && t.Status == appdb.BackupNotConfigured
}

func (s *Server) putObjectArtifact(ctx context.Context, tgt appdb.BackupTarget, artifactID, sourcePath, format string) (objstore.Result, error) {
	pass, enc, err := s.Store.BackupCredentials(ctx, tgt.ClusterID, tgt.ID)
	if err != nil {
		return objstore.Result{}, errUnavailable("backup credentials are unreadable")
	}
	key, err := objstore.ParseKey(enc)
	if err != nil {
		return objstore.Result{}, errBadRequest("client-side encryption key is required; bucket SSE is not sufficient")
	}
	return s.objectRPC().ObjectBackup(ctx, objstore.Request{
		Action: objstore.ActionPut, Provider: tgt.Kind, Endpoint: tgt.Endpoint, Region: tgt.Region,
		Bucket: tgt.Bucket, Key: objstore.ObjectKey(tgt.Prefix, artifactID, format),
		SourcePath: sourcePath, AccessKeyID: tgt.Username, SecretAccessKey: pass, EncryptionKey: key,
		NoCheckBucket: tgt.NoCheckBucket,
	})
}

func (s *Server) materializeArtifact(ctx context.Context, clusterID string, art appdb.BackupArtifact) (string, func(), error) {
	if !strings.HasPrefix(art.Locator, "s3://") && art.ObjectKey == "" {
		return art.Locator, func() {}, nil
	}
	run, _ := s.Store.GetBackupRun(ctx, clusterID, art.RunID)
	if run == nil {
		return "", nil, errUnprocessable("restore cannot locate the original backup target")
	}
	tgt, err := s.Store.GetBackupTarget(ctx, clusterID, run.TargetID)
	if err != nil || tgt == nil {
		return "", nil, errNotFound("backup target not found")
	}
	pass, enc, err := s.Store.BackupCredentials(ctx, clusterID, tgt.ID)
	if err != nil {
		return "", nil, errUnavailable("backup credentials are unreadable")
	}
	key, err := objstore.ParseKey(enc)
	if err != nil {
		return "", nil, errBadRequest("client-side encryption key is required")
	}
	dir, err := os.MkdirTemp("", "ndl-restore-")
	if err != nil {
		return "", nil, err
	}
	dest := filepath.Join(dir, art.ID+".qcow2")
	objectKey := art.ObjectKey
	if objectKey == "" {
		objectKey = strings.TrimPrefix(art.Locator, "s3://"+tgt.Bucket+"/")
	}
	_, err = s.objectRPC().ObjectBackup(ctx, objstore.Request{
		Action: objstore.ActionGet, Provider: tgt.Kind, Endpoint: tgt.Endpoint, Region: tgt.Region,
		Bucket: tgt.Bucket, Key: objectKey, DestPath: dest,
		AccessKeyID: tgt.Username, SecretAccessKey: pass, EncryptionKey: key,
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return dest, func() { _ = os.RemoveAll(dir) }, nil
}
