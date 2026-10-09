package main

import (
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// watchFDs logs the agent's open file descriptors when they climb past half
// of its limit, with a breakdown by kind, so a leak shows up in the journal
// long before work starts failing with "too many open files".
func watchFDs() {
	reported := 0
	for {
		time.Sleep(5 * time.Minute)
		var lim syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil || lim.Cur == 0 {
			continue
		}
		kinds, total := openFDs()
		if total < int(lim.Cur/2) || total < reported+reported/10 {
			continue
		}
		reported = total
		names := make([]string, 0, len(kinds))
		for k := range kinds {
			names = append(names, k)
		}
		sort.Slice(names, func(i, j int) bool { return kinds[names[i]] > kinds[names[j]] })
		parts := make([]string, 0, len(names))
		for _, k := range names {
			parts = append(parts, k+"="+strconv.Itoa(kinds[k]))
		}
		log.Printf("file descriptors: %d open of %d allowed (%s)", total, lim.Cur, strings.Join(parts, " "))
	}
}

func openFDs() (map[string]int, int) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return nil, 0
	}
	kinds := map[string]int{}
	for _, e := range entries {
		target, err := os.Readlink("/proc/self/fd/" + e.Name())
		if err != nil {
			continue
		}
		kind := "file"
		switch {
		case strings.HasPrefix(target, "socket:"):
			kind = "socket"
		case strings.HasPrefix(target, "pipe:"):
			kind = "pipe"
		case strings.HasPrefix(target, "anon_inode:"):
			kind = strings.TrimPrefix(target, "anon_inode:")
		case strings.HasPrefix(target, "/dev/pts/"), target == "/dev/ptmx":
			kind = "pty"
		}
		kinds[kind]++
	}
	return kinds, len(entries)
}
