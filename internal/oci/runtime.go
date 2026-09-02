package oci

import "context"

// Runtime is the OCI container runtime. Production uses containerd via allowlisted ctr argv.
type Runtime interface {
	Pull(ctx context.Context, req PullRequest) (digest string, err error)
	Run(ctx context.Context, spec Spec) error
	Stop(ctx context.Context, workloadID string) error
	Observe(ctx context.Context, workloadID string) (Observed, error)
	Delete(ctx context.Context, workloadID string) error
}

// Ensure FakeRuntime and Containerd implement Runtime.
var (
	_ Runtime = (*FakeRuntime)(nil)
	_ Runtime = (*Containerd)(nil)
)
