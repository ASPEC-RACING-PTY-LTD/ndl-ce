package identity

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Files is durable on-disk cluster and node identity.
type Files struct {
	Dir string
}

type clusterDoc struct {
	ClusterID string `json:"cluster_id"`
}

type nodeDoc struct {
	NodeID    string `json:"node_id"`
	ClusterID string `json:"cluster_id"`
}

// ClusterPath is cluster.json.
func (f Files) ClusterPath() string { return filepath.Join(f.Dir, "cluster.json") }

// NodePath is node.json.
func (f Files) NodePath() string { return filepath.Join(f.Dir, "node.json") }

// HostKeyPath is the recover-admin host key.
func (f Files) HostKeyPath() string { return filepath.Join(f.Dir, "host.key") }

// SetupTokenPath is the root-only plaintext setup token.
func (f Files) SetupTokenPath() string { return filepath.Join(f.Dir, "setup.token") }

// LoadCluster returns the persisted cluster id.
func (f Files) LoadCluster() (string, error) {
	return readID(f.ClusterPath(), "cluster_id")
}

// LoadNode returns persisted node and cluster ids.
func (f Files) LoadNode() (nodeID, clusterID string, err error) {
	b, err := os.ReadFile(f.NodePath())
	if err != nil {
		return "", "", err
	}
	var doc nodeDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", "", err
	}
	if doc.NodeID == "" {
		return "", "", errors.New("node.json missing node_id")
	}
	return doc.NodeID, doc.ClusterID, nil
}

// SaveCluster writes cluster.json without changing an existing id.
func (f Files) SaveCluster(id string) error {
	if existing, err := f.LoadCluster(); err == nil {
		if existing != id {
			return fmt.Errorf("cluster_id already %s", existing)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeJSON(f.ClusterPath(), clusterDoc{ClusterID: id}, 0640)
}

// SaveNode writes node.json. Existing node_id is reused.
func (f Files) SaveNode(nodeID, clusterID string) error {
	if existing, existingCluster, err := f.LoadNode(); err == nil {
		if existing != nodeID {
			return fmt.Errorf("node_id already %s", existing)
		}
		if existingCluster != "" && existingCluster != clusterID {
			return fmt.Errorf("node already enrolled in cluster %s", existingCluster)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeJSON(f.NodePath(), nodeDoc{NodeID: nodeID, ClusterID: clusterID}, 0640)
}

// SaveJoinMaterial persists HTTP join identity and node mTLS material. The CA private key is not included.
func (f Files) SaveJoinMaterial(clusterID, nodeID string, caPEM, certPEM, keyPEM []byte) error {
	if err := f.SaveCluster(clusterID); err != nil {
		return err
	}
	if err := f.SaveNode(nodeID, clusterID); err != nil {
		return err
	}
	if err := os.MkdirAll(f.Dir, 0750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(f.Dir, "cluster-ca.crt"), caPEM, 0640); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(f.Dir, "node.crt"), certPEM, 0640); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.Dir, "node.key"), keyPEM, 0600)
}

// EnsureHostKey creates host.key if missing.
func (f Files) EnsureHostKey() ([]byte, error) {
	if b, err := os.ReadFile(f.HostKeyPath()); err == nil && len(b) >= 32 {
		return b, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(f.Dir, 0750); err != nil {
		return nil, err
	}
	if err := os.WriteFile(f.HostKeyPath(), b, 0600); err != nil {
		return nil, err
	}
	return b, nil
}

func readID(path, field string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var raw map[string]string
	if err := json.Unmarshal(b, &raw); err != nil {
		return "", err
	}
	id := raw[field]
	if id == "" {
		return "", fmt.Errorf("%s missing %s", path, field)
	}
	return id, nil
}

func writeJSON(path string, v any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, mode)
}
