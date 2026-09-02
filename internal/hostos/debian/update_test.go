package debian

import (
	"strings"
	"testing"
)

func TestUpdateArgvNeverShell(t *testing.T) {
	check := CheckArgv()
	if strings.Contains(strings.Join(check, " "), "bash") || check[0] != "/usr/bin/apt-get" {
		t.Fatalf("%v", check)
	}
	if !strings.Contains(strings.Join(check, " "), "AllowUnauthenticated=false") {
		t.Fatal("signed repo required")
	}
	pol, err := PolicyArgv("ndl-control")
	if err != nil || pol[len(pol)-1] != "ndl-control" {
		t.Fatalf("%v %v", pol, err)
	}
	if _, err := PolicyArgv("bash"); err == nil {
		t.Fatal("bash package")
	}
	apply := ApplyArgv(true)
	if !contains(apply, "--dry-run") || !contains(apply, "nodal") {
		t.Fatalf("%v", apply)
	}
	rb, err := RollbackControlArgv("0.1.10", true)
	if err != nil || !contains(rb, "ndl-control=0.1.10") {
		t.Fatalf("%v %v", rb, err)
	}
	if _, err := RollbackControlArgv("1; rm -rf /", false); err == nil {
		t.Fatal("injection")
	}
	tar, err := CheckpointTarArgv("/var/lib/ndl/update-checkpoints/x.tar")
	if err != nil || tar[0] != "/usr/bin/tar" {
		t.Fatal(tar, err)
	}
	parsed := ParsePolicy("ndl-control:\n  Installed: 0.1.10\n  Candidate: 0.1.11\n")
	if parsed.Installed != "0.1.10" || parsed.Candidate != "0.1.11" {
		t.Fatalf("%+v", parsed)
	}
	joined := strings.Join(ApplyArgv(false), " ")
	if strings.Contains(joined, "systemctl") || strings.Contains(joined, "qemu") || strings.Contains(joined, "nodal-vm") {
		t.Fatalf("apply must not touch guests: %s", joined)
	}
	if contains(GRUBPreviousArgv(), "install") {
		t.Fatal("grub must stay a kernel rollback argv")
	}
	feat, err := FeatureInstallArgv("nodal-feature-k8s", true)
	if err != nil || !contains(feat, "nodal-feature-k8s") || contains(feat, "kubelet") || contains(feat, "kubeadm") {
		t.Fatalf("%v %v", feat, err)
	}
	if _, err := FeatureInstallArgv("kubeadm", false); err == nil {
		t.Fatal("kubeadm must not be a feature package")
	}
	if _, err := FeatureInstallArgv("bash", false); err == nil {
		t.Fatal("bash package")
	}
	rm, err := FeatureRemoveArgv("nodal-feature-oci", false)
	if err != nil || contains(rm, "purge") || contains(rm, "kubelet") {
		t.Fatalf("%v %v", rm, err)
	}
	joinedFeat := strings.Join(feat, " ")
	if strings.Contains(joinedFeat, "systemctl") || strings.Contains(joinedFeat, "kubelet") {
		t.Fatalf("feature install must not start k8s: %s", joinedFeat)
	}
	start := K8sRuntimeArgv(true)
	if start[0] != "/usr/bin/systemctl" || !contains(start, "start") || !contains(start, "kubelet") {
		t.Fatalf("%v", start)
	}
	stop := K8sRuntimeArgv(false)
	if !contains(stop, "stop") || contains(stop, "bash") {
		t.Fatalf("%v", stop)
	}
}

func contains(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}
