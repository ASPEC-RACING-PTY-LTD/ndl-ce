package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/no-dal/ndl-ce/internal/lxc"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ndl-lxc-nesting-apparmor:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	hook := os.Getenv("LXC_HOOK_TYPE")
	name := os.Getenv("LXC_NAME")
	cfg := os.Getenv("LXC_CONFIG_FILE")
	if len(args) >= 4 {
		name = args[0]
		cfg = args[1]
		hook = args[3]
	} else if len(args) == 2 {
		return lxc.ApplyGeneratedNestingProfile(args[0], args[1], lxc.LiveApparmorReload)
	}
	if hook != "" && hook != "start-host" {
		return nil
	}
	if cfg == "" || name == "" {
		return fmt.Errorf("usage: ndl-lxc-nesting-apparmor CONTAINER-DIR LXC-NAME")
	}
	return lxc.ApplyGeneratedNestingProfile(filepath.Dir(cfg), name, lxc.LiveApparmorReload)
}
