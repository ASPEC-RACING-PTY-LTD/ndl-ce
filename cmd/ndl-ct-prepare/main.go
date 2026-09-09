package main

import (
	"context"
	"fmt"
	"os"

	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ndl-ct-prepare:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 || args[0] == "" {
		return fmt.Errorf("usage: ndl-ct-prepare WORKLOAD-UUID")
	}
	e := &lxc.Engine{}
	applied, err := e.LastApplied(args[0])
	if err != nil {
		return nil
	}
	rootfs := applied.Spec.RootfsPath
	if rootfs == "" {
		return nil
	}
	d := storage.Directory{Run: storage.LiveRun}
	return d.EnsureDirectoryRootMounted(context.Background(), rootfs)
}
