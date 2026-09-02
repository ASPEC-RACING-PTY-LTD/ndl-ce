package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/no-dal/ndl-ce/internal/guest"
)

func main() {
	socket := flag.String("socket", "", "unix socket for tests or vsock-less labs")
	root := flag.String("root", "", "files jail (tests only; product uses guest /)")
	osName := flag.String("os", runtime.GOOS, "guest OS name reported by guest.info")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h := &guest.Host{OS: *osName, Arch: runtime.GOARCH, Version: guest.Version}
	if *root != "" {
		h.Root = *root
		h.FakePTY = true
	}
	addr := *socket
	network := "unix"
	if addr == "" {
		addr = defaultChannel()
		network = "file"
	}
	if err := guest.ListenAndServe(ctx, network, addr, h); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func defaultChannel() string {
	if runtime.GOOS == "windows" {
		return `\\.\Global\org.nodal.guest.0`
	}
	return "/dev/virtio-ports/org.nodal.guest.0"
}
