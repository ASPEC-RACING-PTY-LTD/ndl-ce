package migration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const StagingOwnerFile = ".ndl-owned"
const StagingOwnerName = "ndl-ce"
const StagingOwnerKind = "migration-staging"

type stagingOwner struct {
	Owner string `json:"owner"`
	Kind  string `json:"kind"`
	JobID string `json:"job_id"`
}

func StagingDir(root, jobID string) (string, error) {
	if err := validateJobID(jobID); err != nil {
		return "", err
	}
	if root == "" {
		root = StagingRoot
	}
	dir := filepath.Join(root, jobID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	if err := ClaimStaging(dir, jobID); err != nil {
		return "", err
	}
	return dir, nil
}

func ClaimStaging(dir, jobID string) error {
	body, err := json.Marshal(stagingOwner{Owner: StagingOwnerName, Kind: StagingOwnerKind, JobID: jobID})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, StagingOwnerFile), append(body, '\n'), 0o640)
}

func StagingOwned(dir, jobID string) bool {
	body, err := os.ReadFile(filepath.Join(dir, StagingOwnerFile))
	if err != nil {
		// Legacy job directories under the No-dal staging root have no marker.
		return true
	}
	var owner stagingOwner
	if json.Unmarshal(body, &owner) != nil {
		return false
	}
	if !strings.EqualFold(owner.Owner, StagingOwnerName) || owner.Kind != StagingOwnerKind {
		return false
	}
	return owner.JobID == "" || owner.JobID == jobID
}

func RemoveStaging(root, jobID string) error {
	if err := validateJobID(jobID); err != nil {
		return err
	}
	if root == "" {
		root = StagingRoot
	}
	dir := filepath.Join(root, jobID)
	if !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(root)+string(os.PathSeparator)) {
		return fmt.Errorf("staging path escape refused")
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !StagingOwned(dir, jobID) {
		return fmt.Errorf("refusing to remove staging that No-dal does not own")
	}
	return os.RemoveAll(dir)
}

func validateJobID(id string) error {
	if id == "" || strings.Contains(id, "..") || strings.ContainsAny(id, "/\\ \n\r") {
		return fmt.Errorf("migration job id is invalid")
	}
	return nil
}
