//go:build !linux

package inventory

func liveInterfaceAddresses() map[string][]string {
	return nil
}
