package main

import (
	"log"
	"os"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/identity"
)

func main() {
	dir := os.Getenv("NODAL_DATA_DIR")
	if dir == "" {
		dir = "/var/lib/ndl"
	}
	h := &agentrpc.Handler{Ident: identity.Files{Dir: dir}}
	if err := agentrpc.Serve(h); err != nil {
		log.Fatal(err)
	}
}
