package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/no-dal/ndl-ce/internal/qemu"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: ndl-qemu-launch WORKLOAD-UUID")
		os.Exit(2)
	}
	id := os.Args[1]
	if err := qemu.ValidateWorkloadID(id); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	path := filepath.Join("/var/lib/ndl/workloads", id, "qemu-argv.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var file qemu.ArgvFile
	if err := json.Unmarshal(raw, &file); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if file.WorkloadID != id {
		fmt.Fprintln(os.Stderr, "frozen argv workload id does not match the unit instance")
		os.Exit(1)
	}
	if err := qemu.ValidateFrozenArgv(id, file.Argv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := syscall.Exec(file.Argv[0], file.Argv, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
