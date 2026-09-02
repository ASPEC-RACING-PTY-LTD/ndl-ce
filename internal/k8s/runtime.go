package k8s

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	ProcKubelet    = "kubelet"
	ProcAPIServer  = "kube-apiserver"
	StatusAbsent   = "absent"
	StatusDetected = "detected"
	DefaultReason  = "No kubelet or kube-apiserver process. Kubernetes is optional and is not the foundation."
	DetectedReason = "A Kubernetes process is running. Default No-dal install does not start kubelet."
	DisabledReason = "Kubernetes is not enabled. Virtual machines and system containers do not require it."
	StartConfirm   = "start-kubelet"
)

// Status is an honest kube-process observation. It is not a claim that No-dal started kubelet.
type Status struct {
	KubeProcess bool     `json:"kube_process"`
	Names       []string `json:"names,omitempty"`
	State       string   `json:"state"`
	Reason      string   `json:"reason"`
}

// ListProcs lists process comm names. Tests inject a fake.
type ListProcs func() []string

// ProcComm lists /proc/*/comm. Missing /proc is treated as no kube process.
func ProcComm() []string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil {
			continue
		}
		n := strings.TrimSpace(string(raw))
		if n != "" {
			names = append(names, n)
		}
	}
	return names
}

// Observe reports whether kubelet or kube-apiserver is present.
func Observe(list ListProcs) Status {
	if list == nil {
		list = ProcComm
	}
	names := list()
	var found []string
	for _, n := range names {
		switch n {
		case ProcKubelet, ProcAPIServer:
			found = append(found, n)
		}
	}
	if len(found) == 0 {
		return Status{State: StatusAbsent, Reason: DefaultReason}
	}
	return Status{KubeProcess: true, Names: found, State: StatusDetected, Reason: DetectedReason}
}
