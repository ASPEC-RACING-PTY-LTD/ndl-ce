package gpu

import "encoding/json"

// Agent actions that act on a workload's full GPU device list rather than
// one claim.
const (
	// ActionReapply rewrites the guest config with the given device nodes.
	ActionReapply = "reapply"
	// ActionDiagnose compares saved devices, config, host, and guest.
	ActionDiagnose = "diagnose"
	// ActionRelease drops one device-node claim. DeviceNodes is the complete
	// list the workload keeps, so the guest's other GPUs survive. An agent
	// that predates it handles it as an assign of that list, which yields
	// the same config.
	ActionRelease = "release"
)

// StatusReleased is the agent result of ActionRelease.
const StatusReleased = "released"

// AssignRequest is the typed agent payload. No generic argv.
type AssignRequest struct {
	Action      string   `json:"action"`
	GPUID       string   `json:"gpu_id"`
	WorkloadID  string   `json:"workload_id"`
	Mode        string   `json:"mode"`
	Exclusive   bool     `json:"exclusive"`
	PCIDevices  []string `json:"pci_devices"`
	DeviceNodes []string `json:"device_nodes"`
	ACSOverride bool     `json:"acs_override"`
	DryRun      bool     `json:"dry_run"`
}

// AssignResult is honest apply outcome.
type AssignResult struct {
	Status        string   `json:"status"`
	Reason        string   `json:"reason,omitempty"`
	PCIDevices    []string `json:"pci_devices,omitempty"`
	DeviceNodes   []string `json:"device_nodes,omitempty"`
	Argv          []string `json:"argv,omitempty"`
	CUDA          string   `json:"cuda,omitempty"`
	ROCm          string   `json:"rocm,omitempty"`
	HostSupported bool     `json:"host_supported,omitempty"`
	Packages      []string `json:"packages,omitempty"`
	// Diagnosis is the engine's device comparison for ActionDiagnose.
	Diagnosis json.RawMessage `json:"diagnosis,omitempty"`
}
