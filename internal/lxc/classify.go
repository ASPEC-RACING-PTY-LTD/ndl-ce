package lxc

import "strings"

// ClassifyEdit reports how each changed CT field applies.
// CPU and memory are live cgroup2 writes. Hostname and IP wait for the next
// start so LXC net/uts keys load. MAC and shrink require a stopped guest.
func ClassifyEdit(prev Spec, next Spec, diskGrow bool, diskShrink bool) []ApplyClass {
	var out []ApplyClass
	add := func(field, apply, reason string, changed bool) {
		if changed {
			out = append(out, ApplyClass{Field: field, Apply: apply, Reason: reason})
		}
	}
	add("cpus", ApplyLive, "cgroup2 cpu.max can change while the container runs", prev.CPUs != next.CPUs)
	add("memory_bytes", ApplyLive, "cgroup2 memory.max can change while the container runs", prev.MemoryBytes != next.MemoryBytes)
	add("name", ApplyRestart, "lxc.uts.name is applied at start; /etc/hostname waits for the next restart", strings.TrimSpace(prev.Name) != strings.TrimSpace(next.Name))
	add("ip", ApplyRestart, "LXC network keys load at start", !prev.IP.Equal(next.IP))
	add("mac", ApplyStop, "MAC changes require a stopped container", !sameMACValue(prev.MAC, next.MAC))
	add("disk_bytes", ApplyLive, "directory roots can grow online without unmounting", diskGrow)
	if diskShrink {
		out = append(out, ApplyClass{Field: "disk_bytes", Apply: ApplyUnsupported, Reason: "shrinking a container disk is not supported"})
	}
	return out
}

func RequiresStop(classes []ApplyClass) bool {
	for _, c := range classes {
		if c.Apply == ApplyStop || c.Apply == ApplyUnsupported {
			return true
		}
	}
	return false
}

func RequiresRestart(classes []ApplyClass) bool {
	for _, c := range classes {
		if c.Apply == ApplyRestart {
			return true
		}
	}
	return false
}

func HasUnsupported(classes []ApplyClass) bool {
	for _, c := range classes {
		if c.Apply == ApplyUnsupported {
			return true
		}
	}
	return false
}

func LiveFields(classes []ApplyClass) []string {
	var out []string
	for _, c := range classes {
		if c.Apply == ApplyLive {
			out = append(out, c.Field)
		}
	}
	return out
}

func sameMACValue(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
