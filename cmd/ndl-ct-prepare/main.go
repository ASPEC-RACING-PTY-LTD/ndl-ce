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
	return prepareCT(&lxc.Engine{}, args)
}

func prepareCT(e *lxc.Engine, args []string) error {
	if len(args) != 1 || args[0] == "" {
		return fmt.Errorf("usage: ndl-ct-prepare WORKLOAD-UUID")
	}
	if err := e.RewriteRuntimeConfig(args[0]); err != nil {
		return err
	}
	applied, err := e.LastApplied(args[0])
	if err != nil {
		return nil
	}
	d := storage.Directory{Run: storage.LiveRun}
	rootfs := applied.Spec.RootfsPath
	if rootfs != "" {
		if err := d.EnsureDirectoryRootMounted(context.Background(), rootfs); err != nil {
			return err
		}
	}
	// Storage mounts: remount image-backed pool folders, then refuse to
	// start while a pool filesystem is missing rather than bind the empty
	// folder underneath it.
	for _, m := range applied.Spec.Mounts {
		if err := d.EnsureDirectoryRootMounted(context.Background(), m.Source); err != nil {
			return err
		}
	}
	if err := lxc.CheckMountsReady(applied.Spec); err != nil {
		return err
	}
	return e.ApplyGuestFiles(args[0])
}
