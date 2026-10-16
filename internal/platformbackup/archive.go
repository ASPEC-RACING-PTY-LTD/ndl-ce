package platformbackup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

// Archive members.
const (
	ManifestFile  = "manifest.json"
	DatabaseFile  = "db.dump"
	RepoKeyFile   = "keys/backup-master.key"
	ConfigDirName = "etc/ndl/"
)

// Manifest describes a snapshot. It is inside the encrypted archive.
type Manifest struct {
	Version int `json:"version"`
	// SchemaLevel is the newest database migration applied; a host can
	// recover a snapshot only at the same level or newer.
	SchemaLevel int           `json:"schema_level"`
	CreatedAt   time.Time     `json:"created_at"`
	ClusterID   string        `json:"cluster_id"`
	Hostname    string        `json:"hostname"`
	Workloads   []WorkloadRef `json:"workloads"`
	Targets     []TargetRef   `json:"targets"`
	Pools       []PoolRef     `json:"pools,omitempty"`
	Networks    []NetworkRef  `json:"networks,omitempty"`
	Nodes       []NodeRef     `json:"nodes,omitempty"`
}

// WorkloadRef is a workload and its restore points at snapshot time.
type WorkloadRef struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	Protected bool       `json:"protected"`
	Points    []PointRef `json:"points,omitempty"`
}

// PointRef is a workload restore point.
type PointRef struct {
	ArtifactID  string    `json:"artifact_id"`
	BackupID    string    `json:"backup_id"`
	Namespace   string    `json:"namespace"`
	TargetID    string    `json:"target_id"`
	RemoteState string    `json:"remote_state"`
	CaptureMode string    `json:"capture_mode,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// TargetRef is a backup target without its credentials.
type TargetRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Endpoint string `json:"endpoint,omitempty"`
	Bucket   string `json:"bucket,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
}

// PoolRef is a storage pool the snapshot's workloads used.
type PoolRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Backend string `json:"backend"`
	Path    string `json:"path,omitempty"`
}

// NetworkRef is a network the snapshot's workloads used.
type NetworkRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Bridge string `json:"bridge,omitempty"`
	CIDR   string `json:"cidr,omitempty"`
}

// NodeRef is a host of the old cluster.
type NodeRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// BuildArchive packs the manifest and files into a gzip tar. Names are
// sorted so the archive is reproducible.
func BuildArchive(m Manifest, files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	man, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	all := map[string][]byte{ManifestFile: man}
	for name, body := range files {
		all[name] = body
	}
	names := make([]string, 0, len(all))
	for name := range all {
		if !safeMember(name) {
			return nil, fmt.Errorf("invalid archive member %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := all[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), ModTime: m.CreatedAt, Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(body); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func safeMember(name string) bool {
	return name != "" && !strings.HasPrefix(name, "/") && path.Clean(name) == name && !strings.HasPrefix(name, "../") && name != ".."
}

// maxMember bounds one archive member when reading.
const maxMember = 4 << 30

// ReadArchive unpacks an archive made by BuildArchive.
func ReadArchive(raw []byte) (Manifest, map[string][]byte, error) {
	var m Manifest
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return m, nil, err
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, nil, err
		}
		if h.Typeflag != tar.TypeReg || !safeMember(h.Name) || h.Size > maxMember {
			return m, nil, fmt.Errorf("unexpected archive member %q", h.Name)
		}
		body, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return m, nil, err
		}
		files[h.Name] = body
	}
	man, ok := files[ManifestFile]
	if !ok {
		return m, nil, fmt.Errorf("the snapshot has no manifest")
	}
	if err := json.Unmarshal(man, &m); err != nil {
		return m, nil, err
	}
	delete(files, ManifestFile)
	return m, files, nil
}
