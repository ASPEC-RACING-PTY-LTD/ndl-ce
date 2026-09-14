package hostos

import (
	"context"
	"strings"
	"testing"
)

func TestEvaluateUpdateUnsupportedUbuntu(t *testing.T) {
	p, err := DetectFrom(strings.NewReader("ID=ubuntu\nVERSION_ID=24.04\nPRETTY_NAME=\"Ubuntu 24.04\"\n"), "x86_64")
	if err == nil {
		t.Fatal("ubuntu must be unsupported")
	}
	res := EvaluateUpdate(p, UpdateRequest{Action: "check", DryRun: true})
	if res.Supported {
		t.Fatal("ubuntu must not be treated as a supported update host")
	}
	if res.Reason != UpdateUnsupportedReason {
		t.Fatalf("reason=%q", res.Reason)
	}
	body := res.Reason + res.Changelog
	if strings.Contains(strings.ToLower(body), "apt-get") || strings.Contains(strings.ToLower(body), "dpkg") {
		t.Fatalf("public reason leaked package manager verb: %q", body)
	}
}

func TestEvaluateUpdateDebian13(t *testing.T) {
	p, err := DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	res := EvaluateUpdate(p, UpdateRequest{Action: "check"})
	if !res.Supported {
		t.Fatalf("%+v", res)
	}
}

func TestRunUpdateStatusDoesNotRefreshIndexes(t *testing.T) {
	p, err := DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	exec := func(_ context.Context, argv []string) (string, error) {
		calls = append(calls, append([]string{}, argv...))
		return "ndl-control:\n  Installed: 0.1.10\n  Candidate: 0.1.10\n", nil
	}
	res, err := RunUpdate(context.Background(), p, UpdateRequest{Action: "status"}, exec)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Supported {
		t.Fatal(res)
	}
	for _, argv := range calls {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, "update") && strings.Contains(joined, "apt-get") {
			t.Fatalf("status must not refresh indexes: %v", argv)
		}
	}
}

func TestRunUpdateSkipExecIsHonest(t *testing.T) {
	p, err := DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	res, err := RunUpdate(context.Background(), p, UpdateRequest{Action: "apply", DryRun: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Supported || res.Status == "succeeded" {
		t.Fatalf("nil exec must not claim succeeded: %+v", res)
	}
	if res.Status != "planned" && res.Status != "unavailable" {
		t.Fatalf("nil exec status=%q want planned or unavailable", res.Status)
	}
	if !strings.Contains(strings.ToLower(res.Reason), "not run") {
		t.Fatalf("reason must say commands were not run: %q", res.Reason)
	}
	if strings.Contains(strings.ToLower(res.Reason), "stop") {
		t.Fatalf("apply must not talk about stopping guests: %q", res.Reason)
	}
	rb, err := RunUpdate(context.Background(), p, UpdateRequest{Action: "rollback", Version: "0.1.10"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rb.Status == "succeeded" {
		t.Fatalf("rollback nil exec must not claim succeeded: %+v", rb)
	}
	cp, err := RunUpdate(context.Background(), p, UpdateRequest{Action: "checkpoint", CheckpointID: "cp1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cp.Status == "succeeded" {
		t.Fatalf("checkpoint nil exec must not claim succeeded: %+v", cp)
	}
}

func TestRunUpdatePreflightIncludesStoreHook(t *testing.T) {
	p, _ := DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
	res, err := RunUpdate(context.Background(), p, UpdateRequest{Action: "preflight"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range res.Checks {
		if c.Name == "store_compatibility" {
			found = true
			if c.Status != "ok" || !strings.Contains(c.Detail, "Helper scripts") {
				t.Fatalf("%+v", c)
			}
		}
	}
	if !found {
		t.Fatal("store hook missing")
	}
}

func TestRunUpdateNilExecDoesNotStartK8sOrOSD(t *testing.T) {
	p, err := DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	k8s, err := RunUpdate(context.Background(), p, UpdateRequest{Action: UpdateK8sRuntimeStart}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if k8s.Status == "succeeded" {
		t.Fatalf("nil exec must not start kubelet: %+v", k8s)
	}
	osd, err := RunUpdate(context.Background(), p, UpdateRequest{Action: UpdateOSDStart}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if osd.Status == "succeeded" {
		t.Fatalf("nil exec must not start ceph-osd: %+v", osd)
	}
}
