package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/oci"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: ndl-oci-launch WORKLOAD-UUID")
		os.Exit(2)
	}
	id := os.Args[1]
	if _, err := uuid.Parse(id); err != nil {
		fmt.Fprintln(os.Stderr, "workload id must be a UUID")
		os.Exit(2)
	}
	e := &oci.Engine{}
	if err := e.LaunchFromApplied(context.Background(), id); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
