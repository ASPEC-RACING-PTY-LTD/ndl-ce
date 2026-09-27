//go:build !linux

package lxc

func cgroupRuleFromStat(string) string { return "" }
