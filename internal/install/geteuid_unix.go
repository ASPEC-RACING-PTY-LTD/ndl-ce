//go:build unix

package install

import "os"

func euid() int { return os.Geteuid() }
