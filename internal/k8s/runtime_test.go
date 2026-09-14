package k8s

import "testing"

func TestObserveDefaultHasNoKubeProcess(t *testing.T) {
	st := Observe(func() []string { return []string{"ndl-control", "qemu-system-x86_64", "systemd"} })
	if st.KubeProcess || st.State != StatusAbsent {
		t.Fatalf("%+v", st)
	}
}

func TestObserveDetectsKubeletWithoutClaimingStart(t *testing.T) {
	st := Observe(func() []string { return []string{"kubelet"} })
	if !st.KubeProcess || st.State != StatusDetected {
		t.Fatalf("%+v", st)
	}
	if st.Reason == "" {
		t.Fatal("reason")
	}
}
