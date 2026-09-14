package inventory

import (
	"strconv"
	"strings"
)

func collectCPU(opt Options) CPU {
	fs := opt.fs()
	out := CPU{
		Architecture: normalizeArch(opt.arch()),
	}

	info := fs.readOK("proc/cpuinfo")
	vendor, model, virt := parseCPUInfo(info)
	out.Vendor = vendor
	out.Model = model
	out.VirtCapability = virt

	online := onlineCPUNames(fs)
	if n := parseLinuxListCount(fs.readOK("sys/devices/system/cpu/online")); n > 0 {
		out.Online = n
	} else if len(online) > 0 {
		out.Online = len(online)
	} else if n := countCPUInfoProcessors(info); n > 0 {
		out.Online = n
	}

	sockets := map[string]struct{}{}
	cores := map[string]struct{}{}
	logical := 0
	for _, name := range online {
		logical++
		pkg := fs.readOK("sys/devices/system/cpu/" + name + "/topology/physical_package_id")
		core := fs.readOK("sys/devices/system/cpu/" + name + "/topology/core_id")
		if pkg != "" {
			sockets[pkg] = struct{}{}
		}
		if pkg != "" || core != "" {
			cores[pkg+"/"+core] = struct{}{}
		}
	}
	if logical == 0 {
		logical = out.Online
	}
	if logical > 0 {
		out.Threads = logical
	}
	if len(sockets) > 0 {
		out.Sockets = len(sockets)
	}
	if len(cores) > 0 {
		out.Cores = len(cores)
	}

	if mhz, ok := cpuMaxMHz(fs, online); ok {
		out.MaxMHz = &mhz
	}

	if out.Vendor == "" && out.Model == "" && out.Threads == 0 && out.Online == 0 {
		out.Status = StatusUnavailable
		return out
	}
	out.Status = StatusAvailable
	return out
}

func normalizeArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "x86_64", "amd64":
		return "amd64"
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}

func parseCPUInfo(s string) (vendor, model, virt string) {
	for _, line := range strings.Split(s, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "vendor_id":
			if vendor == "" {
				vendor = val
			}
		case "model name":
			if model == "" {
				model = val
			}
		case "flags", "Features":
			for _, f := range strings.Fields(val) {
				if virt != "" {
					break
				}
				if f == "vmx" {
					virt = "vmx"
				}
				if f == "svm" {
					virt = "svm"
				}
			}
		}
	}
	return vendor, model, virt
}

func countCPUInfoProcessors(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		key, _, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "processor" {
			n++
		}
	}
	return n
}

func onlineCPUNames(fs FS) []string {
	allowed := parseLinuxListSet(fs.readOK("sys/devices/system/cpu/online"))
	var names []string
	for _, name := range fs.list("sys/devices/system/cpu") {
		if !isCPUDir(name) {
			continue
		}
		if len(allowed) > 0 {
			n, err := strconv.Atoi(name[3:])
			if err != nil || !allowed[n] {
				continue
			}
		}
		names = append(names, name)
	}
	return names
}

func parseLinuxListSet(s string) map[int]bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, ranged := strings.Cut(part, "-")
		if !ranged {
			if n, err := strconv.Atoi(part); err == nil {
				out[n] = true
			}
			continue
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil || b < a {
			continue
		}
		for n := a; n <= b; n++ {
			out[n] = true
		}
	}
	return out
}

func isCPUDir(name string) bool {
	if !strings.HasPrefix(name, "cpu") {
		return false
	}
	return allDigits(name[3:])
}

func cpuMaxMHz(fs FS, cpus []string) (float64, bool) {
	candidates := cpus
	if len(candidates) == 0 {
		candidates = []string{"cpu0"}
	}
	for _, name := range candidates {
		raw, ok := fs.readUint("sys/devices/system/cpu/" + name + "/cpufreq/cpuinfo_max_freq")
		if !ok || raw == 0 {
			continue
		}
		return float64(raw) / 1000.0, true
	}
	return 0, false
}

func parseLinuxListCount(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n := 0
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, ranged := strings.Cut(part, "-")
		if !ranged {
			if _, err := strconv.Atoi(part); err == nil {
				n++
			}
			continue
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil || b < a {
			continue
		}
		n += b - a + 1
	}
	return n
}
