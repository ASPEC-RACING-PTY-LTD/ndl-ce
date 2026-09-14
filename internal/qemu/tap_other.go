//go:build !linux

package qemu

import "fmt"

func createTAPDevice(string) error {
	return fmt.Errorf("tap devices require linux")
}
