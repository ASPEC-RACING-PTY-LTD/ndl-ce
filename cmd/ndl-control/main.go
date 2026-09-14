package main

import (
	"log"

	"github.com/no-dal/ndl-ce/internal/control"
)

func main() {
	if err := control.Run(control.LoadConfig()); err != nil {
		log.Fatal(err)
	}
}
