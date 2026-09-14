package inventory

import (
	"strconv"
	"strings"
)

func collectMemory(opt Options) Memory {
	fs := opt.fs()
	out := Memory{DIMMStatus: StatusNotReported}

	totalKB, availKB, hasTotal, hasAvail := parseMeminfo(fs.readOK("proc/meminfo"))
	if !hasTotal {
		out.Status = StatusUnavailable
		return out
	}

	out.Status = StatusAvailable
	out.TotalBytes = totalKB * 1024
	if hasAvail {
		avail := availKB * 1024
		out.AvailableBytes = &avail
		if avail <= out.TotalBytes {
			used := out.TotalBytes - avail
			out.UsedBytes = &used
		}
	}
	return out
}

func parseMeminfo(s string) (totalKB, availKB uint64, hasTotal, hasAvail bool) {
	for _, line := range strings.Split(s, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, ok := parseKBField(rest)
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "MemTotal":
			totalKB = n
			hasTotal = true
		case "MemAvailable":
			availKB = n
			hasAvail = true
		}
	}
	return totalKB, availKB, hasTotal, hasAvail
}

func parseKBField(rest string) (uint64, bool) {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
