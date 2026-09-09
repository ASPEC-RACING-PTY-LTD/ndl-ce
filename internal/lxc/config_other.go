//go:build !linux

package lxc

func cgroupAllowFromStat(string) string { return "" }
