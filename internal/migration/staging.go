package migration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
	return dir, nil
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
	return os.RemoveAll(dir)
}

func validateJobID(id string) error {
	if id == "" || strings.Contains(id, "..") || strings.ContainsAny(id, "/\\ \n\r") {
		return fmt.Errorf("migration job id is invalid")
	}
	return nil
}
