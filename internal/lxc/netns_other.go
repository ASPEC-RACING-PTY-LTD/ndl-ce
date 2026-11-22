//go:build !linux

package lxc

func netnsIPv4(int, string) string { return "" }
