package main

import (
	"encoding/json"
	"testing"

	"github.com/no-dal/ndl-ce/internal/qemu"
)

func TestLaunchRequiresUUID(t *testing.T) {
	for _, id := range []string{"", "../escape", "a/b", "not-a-uuid"} {
		if err := qemu.ValidateWorkloadID(id); err == nil {
			t.Fatalf("id %q must fail", id)
		}
	}
	if err := qemu.ValidateWorkloadID("66666666-6666-4666-8666-666666666666"); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchRevalidatesFrozenArgv(t *testing.T) {
	id := "66666666-6666-4666-8666-666666666666"
	ok := []string{qemu.BinQEMU, "-machine", qemu.DefaultMachine + ",usb=off", "-mon", "chardev=qmp0,mode=control"}
	if err := qemu.ValidateFrozenArgv(id, ok); err != nil {
		t.Fatal(err)
	}
	if err := qemu.ValidateFrozenArgv(id, []string{"/bin/bash", "-c", "true"}); err == nil {
		t.Fatal("shell argv must fail")
	}
	if err := qemu.ValidateFrozenArgv(id, []string{qemu.BinQEMU, "-smp", "1"}); err == nil {
		t.Fatal("missing QMP control monitor must fail")
	}
	mismatch, _ := json.Marshal(qemu.ArgvFile{WorkloadID: "other", Argv: ok})
	var file qemu.ArgvFile
	if err := json.Unmarshal(mismatch, &file); err != nil {
		t.Fatal(err)
	}
	if file.WorkloadID == id {
		t.Fatal("mismatch fixture")
	}
}
