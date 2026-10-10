package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/oci"
)

func main() {
	args := os.Args[1:]
	teardown := len(args) == 2 && args[0] == "--teardown"
	if teardown {
		args = args[1:]
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: ndl-oci-launch [--teardown] WORKLOAD-UUID")
		os.Exit(2)
	}
	id := args[0]
	if _, err := uuid.Parse(id); err != nil {
		fmt.Fprintln(os.Stderr, "workload id must be a UUID")
		os.Exit(2)
	}
	e := &oci.Engine{}
	if teardown {
		// Clears a bridged container's namespace, DHCP client and port rules
		// after the unit stops, even when the launcher itself was killed.
		e.TeardownNetwork(context.Background(), id)
		return
	}
	if err := e.LaunchFromApplied(context.Background(), id); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
