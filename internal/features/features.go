package features

import "strings"

const (
	IDVM           = "vm"
	IDCT           = "ct"
	IDOCI          = "oci"
	IDGPU          = "gpu"
	IDK8s          = "k8s"
	IDDistStorage  = "distributed_storage"
	IDAI           = "ai"
	EnableK8s      = "enable-k8s"
	DisableConfirm = "disable-feature"
	// TinyK8sMemoryBytes is the RAM floor for Kubernetes without confirm.
	// At or below this, enable requires X-Nodal-Confirm: enable-k8s.
	TinyK8sMemoryBytes  uint64 = 8 << 30
	K8sNotStartedReason        = "Kubernetes runtime is optional. Enabling the module does not start kubelet."
	GPUOptionalReason          = "GPU services are optional. Phase 14 assignment stays available without this package."
	DistStorageReason          = "Distributed storage is an optional package set. Ceph is not started here."
	AIReason                   = "AI services are an optional package set. Models are not started here."
	OCIReason                  = "OCI extras are optional. Application workloads from Phase 21 stay available."
	CoreInstalledReason        = "Installed with the nodal metapackage."
	LightBaseReason            = "Fresh install is the nodal metapackage only. GPU, Kubernetes, distributed storage, and AI stay opt-in."
)

// Module is one Settings Features row.
type Module struct {
	ID             string
	Title          string
	Package        string
	Core           bool
	RequiresK8sAck bool
	StartsRuntime  bool
	DefaultReason  string
}

// Catalog is deny-by-default optional modules plus always-on core.
func Catalog() []Module {
	return []Module{
		{ID: IDVM, Title: "Virtual Machines", Core: true, StartsRuntime: true, DefaultReason: CoreInstalledReason},
		{ID: IDCT, Title: "System Containers", Core: true, StartsRuntime: true, DefaultReason: CoreInstalledReason},
		{ID: IDOCI, Title: "OCI Containers", Package: "nodal-feature-oci", DefaultReason: OCIReason},
		{ID: IDGPU, Title: "GPU Services", Package: "nodal-feature-gpu", DefaultReason: GPUOptionalReason},
		{ID: IDK8s, Title: "Kubernetes", Package: "nodal-feature-k8s", RequiresK8sAck: true, DefaultReason: K8sNotStartedReason},
		{ID: IDDistStorage, Title: "Distributed Storage", Package: "nodal-feature-distributed-storage", DefaultReason: DistStorageReason},
		{ID: IDAI, Title: "AI Services", Package: "nodal-feature-ai", DefaultReason: AIReason},
	}
}

// Lookup returns a catalog module. "kubernetes" is an alias for k8s.
func Lookup(id string) (Module, bool) {
	want := strings.TrimSpace(id)
	if want == "kubernetes" {
		want = IDK8s
	}
	for _, m := range Catalog() {
		if m.ID == want {
			return m, true
		}
	}
	return Module{}, false
}

// TinyNode reports whether RAM is at or below the Kubernetes confirm floor.
// Missing or zero memory is treated as tiny.
func TinyNode(memoryBytes uint64) bool {
	return memoryBytes == 0 || memoryBytes <= TinyK8sMemoryBytes
}
