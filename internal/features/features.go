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
	IDDocker       = "docker"
	IDGameServers  = "gameservers"
	IDCloudflare   = "cloudflare"
	IDCaddy        = "caddy"
	IDNginx        = "nginx"
	IDCodehold     = "codehold"
	IDAutomation   = "automation"
	IDDDoS         = "ddos"
	EnableK8s      = "enable-k8s"
	DisableConfirm = "disable-feature"
	// TinyK8sMemoryBytes is the RAM floor for Kubernetes without confirm.
	// At or below this, enable requires X-Nodal-Confirm: enable-k8s.
	TinyK8sMemoryBytes  uint64 = 8 << 30
	K8sNotStartedReason        = "Kubernetes runtime is optional. Enabling the module does not start kubelet."
	GPUOptionalReason          = "GPU services are optional. Phase 14 assignment stays available without this package."
	DistStorageReason          = "Distributed storage is an optional package set. Ceph is not started here."
	AIReason                   = "AI services are an optional package set. Models are not started here."
	OCIReason                  = "OCI extras are optional. Enabling the module installs containerd via nodal-feature-oci."
	DockerReason               = "Docker Management is optional. Enabling it discovers Docker engines on this node and in system containers. It does not install Docker Engine."
	GameServersReason          = "Game Servers is optional. Enabling it does not start servers or delete existing ones when later disabled."
	CloudflareReason           = "Cloudflare is optional. Turning it off only hides it; tokens, tunnels, routes and connectors are kept and keep running."
	AutomationReason           = "Automation is optional. Flows only run once they are published and turned on; disabling it pauses every flow and deletes none."
	CaddyReason                = "Caddy is optional. Turning it on finds Caddy on this node and in system containers. Turning it off only hides it; Caddy and its sites are not changed."
	NginxReason                = "Nginx is optional. Turning it on finds Nginx on this node and in system containers. Turning it off only hides it; Nginx and its sites are not changed."
	CodeholdReason             = "CodeHold runners is optional. Turning it on lets this node host CodeHold Actions runners, one system container each. Turning it off asks what happens to their containers first."
	DDoSReason                 = "DDoS Protection is optional. Turning it on filters floods to game servers, workloads and host services on this node's uplink. Turning it off removes the filter rules and keeps your settings."
	CoreInstalledReason        = "Installed with the nodal metapackage."
	LightBaseReason            = "Fresh install is the nodal metapackage only. GPU, Kubernetes, distributed storage, AI, Docker Management, Game Servers, Cloudflare, Caddy, Nginx, CodeHold runners and DDoS Protection stay opt-in."
)

// Module is one Settings Features row.
type Module struct {
	ID             string
	Title          string
	Package        string
	Core           bool
	RequiresK8sAck bool
	StartsRuntime  bool
	SoftwareOnly   bool
	DefaultReason  string
}

// Catalog is deny-by-default optional modules plus always-on core.
func Catalog() []Module {
	return []Module{
		{ID: IDVM, Title: "Virtual Machines", Core: true, StartsRuntime: true, DefaultReason: CoreInstalledReason},
		{ID: IDCT, Title: "System Containers", Core: true, StartsRuntime: true, DefaultReason: CoreInstalledReason},
		{ID: IDOCI, Title: "OCI Containers", Package: "nodal-feature-oci", DefaultReason: OCIReason},
		{ID: IDDocker, Title: "Docker Management", Package: "nodal-feature-docker", DefaultReason: DockerReason},
		{ID: IDGameServers, Title: "Game Servers", SoftwareOnly: true, DefaultReason: GameServersReason},
		{ID: IDCloudflare, Title: "Cloudflare", SoftwareOnly: true, DefaultReason: CloudflareReason},
		{ID: IDCaddy, Title: "Caddy", SoftwareOnly: true, DefaultReason: CaddyReason},
		{ID: IDNginx, Title: "Nginx", SoftwareOnly: true, DefaultReason: NginxReason},
		{ID: IDCodehold, Title: "CodeHold runners", SoftwareOnly: true, DefaultReason: CodeholdReason},
		{ID: IDAutomation, Title: "Automation", SoftwareOnly: true, DefaultReason: AutomationReason},
		{ID: IDDDoS, Title: "DDoS Protection", SoftwareOnly: true, DefaultReason: DDoSReason},
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
