package main

import (
	"fmt"
	"log"
	"os"

	"github.com/no-dal/ndl-ce/internal/buildinfo"

	"github.com/no-dal/ndl-ce/internal/control"
)

func main() {
	// "version" lets the live updater check a staged binary before it is
	// swapped in.
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("ndl-control", buildinfo.Version, buildinfo.ID())
		return
	}
	if err := control.Run(control.LoadConfig()); err != nil {
		log.Fatal(err)
	}
}
