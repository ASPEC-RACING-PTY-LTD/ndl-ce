package main

import (
	"fmt"
	"os"
)

func versionLine() string {
	return "nodalctl 0.0.0"
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Fprintln(os.Stdout, versionLine())
		return
	}
	fmt.Fprintln(os.Stdout, "nodalctl")
	fmt.Fprintln(os.Stdout, "Phase 0 skeleton. Product commands arrive in later phases.")
}
