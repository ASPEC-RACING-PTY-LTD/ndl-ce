package gpu

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
}
