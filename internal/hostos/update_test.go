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

func TestRunUpdateSkipExecIsHonest(t *testing.T) {
	p, err := DetectFrom(strings.NewReader("ID=debian\nVERSION_ID=13\nPRETTY_NAME=\"Debian GNU/Linux 13\"\n"), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	res, err := RunUpdate(context.Background(), p, UpdateRequest{Action: "apply", DryRun: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Supported || res.Status != "succeeded" {
		t.Fatalf("%+v", res)
	}
	if strings.Contains(strings.ToLower(res.Reason), "stop") {
		t.Fatalf("apply must not talk about stopping guests: %q", res.Reason)
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
			if c.Status != "unsupported" || !strings.Contains(c.Detail, "Phase 36") {
				t.Fatalf("%+v", c)
			}
		}
	}
	if !found {
		t.Fatal("store hook missing")
	}
}
