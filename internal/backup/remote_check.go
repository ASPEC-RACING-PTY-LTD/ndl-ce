package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// RemoteCheck is the outcome of checking one restore point on a target.
type RemoteCheck struct {
	Namespace string `json:"namespace"`
	BackupID  string `json:"backup_id"`
	// Present is set when the manifest and location map exist.
	Present bool `json:"present"`
	// Deep is set when the manifest and location map were downloaded and
	// authenticated, and sampled packs were found.
	Deep         bool   `json:"deep,omitempty"`
	Packs        int    `json:"packs,omitempty"`
	PacksChecked int    `json:"packs_checked,omitempty"`
	Error        string `json:"error,omitempty"`
}

// CheckRemote confirms a restore point can be fetched from target. The quick
// check looks for its manifest and location map. The deep check downloads
// and authenticates both with keys and looks for sample of the packs it
// references (all of them when sample is 0 or more than there are).
func CheckRemote(ctx context.Context, target Target, keys *Keys, namespace, backupID string, deep bool, sample int) RemoteCheck {
	out := RemoteCheck{Namespace: namespace, BackupID: backupID}
	for _, key := range []string{objectKeyManifest(namespace, backupID), objectKeyLocmap(namespace, backupID)} {
		ok, _, err := target.Head(ctx, key)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		if !ok {
			out.Error = "missing " + key
			return out
		}
	}
	out.Present = true
	if !deep {
		return out
	}
	manRaw, err := target.Get(ctx, objectKeyManifest(namespace, backupID))
	if err != nil {
		out.Error = fmt.Sprintf("download manifest: %v", err)
		return out
	}
	if _, err := decodeManifestBytes(keys, namespace, backupID, manRaw); err != nil {
		out.Error = fmt.Sprintf("manifest failed authentication: %v", err)
		return out
	}
	locRaw, err := target.Get(ctx, objectKeyLocmap(namespace, backupID))
	if err != nil {
		out.Error = fmt.Sprintf("download location map: %v", err)
		return out
	}
	packs, err := keys.locmapPacks(namespace, locRaw)
	if err != nil {
		out.Error = fmt.Sprintf("location map failed authentication: %v", err)
		return out
	}
	ids := make([]string, 0, len(packs))
	for id := range packs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out.Packs = len(ids)
	if sample > 0 && sample < len(ids) {
		// Spread the sample over the sorted ids so repeated checks of the
		// same point look at the same packs and big points are covered.
		step := len(ids) / sample
		picked := make([]string, 0, sample)
		for i := 0; i < sample; i++ {
			picked = append(picked, ids[i*step])
		}
		ids = picked
	}
	for _, id := range ids {
		ok, size, err := target.Head(ctx, objectKeyPack(id))
		if err != nil {
			out.Error = err.Error()
			return out
		}
		if !ok || size == 0 {
			out.Error = "missing pack " + id
			return out
		}
		out.PacksChecked++
	}
	out.Deep = true
	return out
}

// decodeManifestBytes authenticates a manifest downloaded from a target.
func decodeManifestBytes(keys *Keys, namespace, backupID string, raw []byte) (*Manifest, error) {
	plain, err := keys.OpenBytes("manifest:"+namespace, raw)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, err
	}
	if m.BackupID != "" && m.BackupID != backupID {
		return nil, fmt.Errorf("manifest is for %s", m.BackupID)
	}
	return &m, nil
}
