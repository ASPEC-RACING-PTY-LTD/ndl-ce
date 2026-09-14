package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backuppack"
	"github.com/no-dal/ndl-ce/internal/objstore"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
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
	if strings.TrimSpace(t.Status) != "" && t.Status != appdb.BackupNotConfigured {
		return t.Status
	}
	if t.NoCheckBucket {
		return appdb.BackupUntested
	}
	if t.Status == "" {
		return appdb.BackupUntested
	}
	return t.Status
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
	if u.User != nil {
		return errBadRequest("endpoint must not include credentials")
	}
	if kind != appdb.BackupMinIO && u.Scheme != "https" {
		return errBadRequest("https endpoint is required except for minio test fixtures")
	}
	return nil
}

func backupTargetAllowsRun(t appdb.BackupTarget) bool {
	switch t.Status {
	case appdb.BackupAvailable, appdb.BackupUntested, appdb.BackupDegraded:
		return true
	}
	return isObjectBackupKind(t.Kind) && t.NoCheckBucket && (t.Status == appdb.BackupNotConfigured || t.Status == appdb.BackupUntested)
}

func (s *Server) objectCreds(ctx context.Context, tgt appdb.BackupTarget) (string, []byte, error) {
	pass, enc, err := s.Store.BackupCredentials(ctx, tgt.ClusterID, tgt.ID)
	if err != nil {
		return "", nil, errUnavailable("backup credentials are unreadable")
	}
	key, err := objstore.ParseKey(enc)
	if err != nil {
		return "", nil, errBadRequest("client-side encryption key is required; bucket SSE is not sufficient")
	}
	return pass, key, nil
}

func (s *Server) putPackArtifact(ctx context.Context, tgt appdb.BackupTarget, sourcePath, prefix, configPath string) (objstore.Result, error) {
	pass, key, err := s.objectCreds(ctx, tgt)
	if err != nil {
		return objstore.Result{}, err
	}
	return s.objectRPC().ObjectBackup(ctx, objstore.Request{
		Action: objstore.ActionPutPack, Provider: tgt.Kind, Endpoint: tgt.Endpoint, Region: tgt.Region,
		Bucket: tgt.Bucket, Key: prefix, SourcePath: sourcePath, DestPath: configPath,
		AccessKeyID: tgt.Username, SecretAccessKey: pass, EncryptionKey: key,
		NoCheckBucket: tgt.NoCheckBucket,
	})
}

func isPackArtifact(art appdb.BackupArtifact) bool {
	if art.Format == backuppack.FormatNDLB {
		return true
	}
	key := strings.Trim(strings.TrimSpace(art.ObjectKey), "/")
	if key == "" {
		key = strings.TrimPrefix(art.Locator, "s3://")
		if i := strings.Index(key, "/"); i >= 0 {
			key = key[i+1:]
		}
	}
	return strings.Contains("/"+key+"/", "/"+backuppack.LayoutRoot+"/")
}

func (s *Server) testBackupTarget(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	tgt, err := s.Store.GetBackupTarget(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || tgt == nil {
		writeErr(w, http.StatusNotFound, "backup target not found")
		return
	}
	if !isObjectBackupKind(tgt.Kind) {
		status := s.probeBackupTarget(r.Context(), tgt.Kind, tgt.Locator)
		_ = s.Store.UpdateBackupTargetStatus(r.Context(), p.User.ClusterID, tgt.ID, status)
		tgt.Status = status
		writeJSON(w, http.StatusOK, backupTargetJSON(*tgt))
		return
	}
	pass, key, err := s.objectCreds(r.Context(), *tgt)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	res, err := s.objectRPC().ObjectBackup(r.Context(), objstore.Request{
		Action: objstore.ActionTest, Provider: tgt.Kind, Endpoint: tgt.Endpoint, Region: tgt.Region,
		Bucket: tgt.Bucket, Key: tgt.Prefix, AccessKeyID: tgt.Username, SecretAccessKey: pass, EncryptionKey: key,
	})
	status := appdb.BackupAvailable
	if err != nil {
		status = firstNonEmpty(res.Status, objstore.Classify(err))
		if status == "" {
			status = appdb.BackupUnavailable
		}
	} else if res.Status != "" {
		status = res.Status
	}
	if err := s.Store.UpdateBackupTargetStatus(r.Context(), p.User.ClusterID, tgt.ID, status); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record backup target")
		return
	}
	tgt.Status = status
	s.audit(r, p.User.ClusterID, p.User.ID, "backup.target.test", status, tgt.ID)
	if err != nil && status != appdb.BackupAvailable {
		writeJSON(w, http.StatusOK, backupTargetJSON(*tgt))
		return
	}
	writeJSON(w, http.StatusOK, backupTargetJSON(*tgt))
}

func (s *Server) materializeArtifact(ctx context.Context, clusterID string, art appdb.BackupArtifact) (string, func(), error) {
	if art.Format == backuppack.FormatNDLB && !strings.HasPrefix(art.Locator, "s3://") && art.ObjectKey == "" {
		dir, err := os.MkdirTemp("", "ndl-restore-")
		if err != nil {
			return "", nil, err
		}
		dest := filepath.Join(dir, art.ID+".tar")
		if _, err := s.Backup.CopyBackup(ctx, qemu.BackupUnpack, art.Locator, dest); err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, err
		}
		return dest, func() { _ = os.RemoveAll(dir) }, nil
	}
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
	pass, key, err := s.objectCreds(ctx, *tgt)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "ndl-restore-")
	if err != nil {
		return "", nil, err
	}
	objectKey := art.ObjectKey
	if objectKey == "" {
		objectKey = strings.TrimPrefix(art.Locator, "s3://"+tgt.Bucket+"/")
	}
	usePack := isPackArtifact(art)
	action := objstore.ActionGet
	ext := firstNonEmpty(art.Format, "qcow2")
	if usePack {
		action = objstore.ActionGetPack
		if art.Format == backuppack.FormatNDLB {
			ext = "tar"
		}
	}
	dest := filepath.Join(dir, art.ID+"."+ext)
	_, err = s.objectRPC().ObjectBackup(ctx, objstore.Request{
		Action: action, Provider: tgt.Kind, Endpoint: tgt.Endpoint, Region: tgt.Region,
		Bucket: tgt.Bucket, Key: objectKey, DestPath: dest,
		AccessKeyID: tgt.Username, SecretAccessKey: pass, EncryptionKey: key,
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return dest, func() { _ = os.RemoveAll(dir) }, nil
}
