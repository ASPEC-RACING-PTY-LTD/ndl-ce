//go:build !unix

package install

func euid() int { return 1 }
