//go:build !linux

package lxc

func cgroupRuleFromStat(string) string { return "" }

func resolveDeviceNode(string) string { return "" }

func charNodeRule(string) string { return "" }
