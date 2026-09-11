package ctbackup

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func syncFrozenRoot(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	_ = unix.Syncfs(int(f.Fd()))
}
