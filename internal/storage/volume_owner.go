package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	VolumeOwnerFile = ".ndl-owned"
	VolumeOwnerName = "ndl-ce"
	VolumeKindMigration = "migration-volume"
	VolumeKindOperator  = "operator-volume"
)

// VolumeOwner is durable metadata written into a Directory volume.
type VolumeOwner struct {
	Owner string `json:"owner"`
	Kind  string `json:"kind"`
	JobID string `json:"job_id,omitempty"`
}

func VolumeOwnerPath(volumeDir string) string {
	return filepath.Join(volumeDir, VolumeOwnerFile)
}

func WriteVolumeOwner(volumeDir string, owner VolumeOwner) error {
	if owner.Owner == "" {
		owner.Owner = VolumeOwnerName
	}
	body, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	return os.WriteFile(VolumeOwnerPath(volumeDir), append(body, '\n'), 0o640)
}

func ReadVolumeOwner(volumeDir string) (VolumeOwner, bool) {
	body, err := os.ReadFile(VolumeOwnerPath(volumeDir))
	if err != nil {
		return VolumeOwner{}, false
	}
	var owner VolumeOwner
	if json.Unmarshal(body, &owner) != nil {
		return VolumeOwner{}, false
	}
	return owner, true
}

func VolumeOwnedByNDL(volumeDir string) bool {
	owner, ok := ReadVolumeOwner(volumeDir)
	if !ok {
		return false
	}
	return strings.EqualFold(owner.Owner, VolumeOwnerName)
}

func MigrationVolumeOwned(volumeDir, jobID string) bool {
	owner, ok := ReadVolumeOwner(volumeDir)
	if !ok {
		return false
	}
	if !strings.EqualFold(owner.Owner, VolumeOwnerName) || owner.Kind != VolumeKindMigration {
		return false
	}
	return jobID == "" || owner.JobID == "" || owner.JobID == jobID
}
