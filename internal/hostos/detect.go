package hostos

import (
	"os"
	"runtime"
)

// Detect reads /etc/os-release and the kernel architecture.
func Detect() (Platform, error) {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return Platform{}, err
	}
	defer f.Close()
	return DetectFrom(f, runtime.GOARCH)
}
