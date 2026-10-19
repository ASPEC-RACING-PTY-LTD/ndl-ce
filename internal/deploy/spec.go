// Package deploy runs a SuperAdmin's update commands for a Docker Compose
// project inside the system container that holds it, streams their output
// to a log file on the host and can stop the whole process tree.
//
// The agent owns a run from start to finish. The control plane starts it,
// reads its status and log by offset, and may cancel it; it never runs the
// commands itself.
package deploy

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Command is one line of a project's update sequence. It runs with bash in
// the project's working folder.
type Command struct {
	Run             string `json:"run"`
	ContinueOnError bool   `json:"continue_on_error,omitempty"`
}

// Spec is one deployment run.
type Spec struct {
	ID         string            `json:"id"`
	MachineID  string            `json:"machine_id"`
	Dir        string            `json:"dir"`
	RunAs      string            `json:"run_as,omitempty"`
	Commands   []Command         `json:"commands"`
	Env        map[string]string `json:"env,omitempty"`
	TimeoutSec int               `json:"timeout_sec,omitempty"`
	LogDir     string            `json:"log_dir"`
}

// Limits on a run.
const (
	MaxCommands       = 50
	MaxCommandBytes   = 16 << 10
	DefaultTimeoutSec = 30 * 60
	MaxTimeoutSec     = 24 * 60 * 60
	ProbeTimeoutSec   = 120
)

var (
	idRE   = regexp.MustCompile(`^[a-f0-9][a-f0-9-]{7,63}$`)
	userRE = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`)
	envRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	// machineRE matches a workload id; the host is not a deployment target.
	machineRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// ValidID reports whether id can name a run, its unit and its files.
func ValidID(id string) bool { return idRE.MatchString(id) }

// ValidUser reports whether u is a plain account name.
func ValidUser(u string) bool { return userRE.MatchString(u) }

// ValidEnvName reports whether k can be exported by a shell.
func ValidEnvName(k string) bool { return envRE.MatchString(k) && !reservedEnv[k] }

var reservedEnv = map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "IFS": true}

// CleanDir validates a working folder inside the container.
func CleanDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if !strings.HasPrefix(dir, "/") || strings.ContainsAny(dir, "\x00\n\r") {
		return "", fmt.Errorf("the working folder must be an absolute path in the container")
	}
	dir = path.Clean(dir)
	if dir == "/" {
		return "", fmt.Errorf("the working folder cannot be the container's root")
	}
	return dir, nil
}

// CleanCommands trims commands and drops empty ones.
func CleanCommands(in []Command) ([]Command, error) {
	out := make([]Command, 0, len(in))
	for _, c := range in {
		c.Run = strings.TrimSpace(strings.ReplaceAll(c.Run, "\r\n", "\n"))
		if c.Run == "" {
			continue
		}
		if strings.ContainsRune(c.Run, 0) || len(c.Run) > MaxCommandBytes {
			return nil, fmt.Errorf("command %d is too long or not text", len(out)+1)
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("add at least one command")
	}
	if len(out) > MaxCommands {
		return nil, fmt.Errorf("a project runs at most %d commands", MaxCommands)
	}
	return out, nil
}

// Validate checks a run before it starts.
func (s *Spec) Validate() error {
	if !ValidID(s.ID) {
		return errors.New("the deployment id is invalid")
	}
	if !machineRE.MatchString(s.MachineID) || s.MachineID == "host" {
		return errors.New("deployments run only in system containers")
	}
	dir, err := CleanDir(s.Dir)
	if err != nil {
		return err
	}
	s.Dir = dir
	if s.RunAs != "" && !ValidUser(s.RunAs) {
		return fmt.Errorf("%q is not a valid user name", s.RunAs)
	}
	if s.Commands, err = CleanCommands(s.Commands); err != nil {
		return err
	}
	for k := range s.Env {
		if !ValidEnvName(k) {
			return fmt.Errorf("%q cannot be set as an environment variable", k)
		}
	}
	if s.TimeoutSec <= 0 {
		s.TimeoutSec = DefaultTimeoutSec
	}
	if s.TimeoutSec > MaxTimeoutSec {
		s.TimeoutSec = MaxTimeoutSec
	}
	return nil
}

// UnitName is the transient systemd unit a run gets in the container.
func UnitName(id string) string { return "ndl-deploy-" + id + ".service" }

// Quote makes s one single-quoted shell word.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
