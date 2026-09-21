package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func metaHash(xattrs map[string]string, holes []Hole) string {
	h := sha256.New()
	if len(xattrs) > 0 {
		keys := make([]string, 0, len(xattrs))
		for k := range xattrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			h.Write([]byte(k))
			h.Write([]byte{0})
			h.Write([]byte(xattrs[k]))
			h.Write([]byte{0})
		}
	}
	for _, hole := range holes {
		h.Write([]byte(strconv.FormatInt(hole.Offset, 10)))
		h.Write([]byte{':'})
		h.Write([]byte(strconv.FormatInt(hole.Length, 10)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fileFingerprint hashes a file's content enough to invalidate the metadata
// cache after a same-size rewrite when timestamps do not move. Small files are
// hashed entirely. Larger files contribute the first and last 4 KiB; middle
// edits still rely on size, mtime, ctime, and inode.
func fileFingerprint(path string, size int64) string {
	if size <= 0 {
		return "empty"
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	_, _ = io.WriteString(h, strconv.FormatInt(size, 10))
	h.Write([]byte{0})
	const window = 4096
	const fullLimit = 1 << 20
	if size <= fullLimit {
		if _, err := io.Copy(h, f); err != nil {
			return ""
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	head := make([]byte, window)
	n, _ := io.ReadFull(f, head)
	if n > 0 {
		h.Write(head[:n])
	}
	if _, err := f.Seek(size-window, io.SeekStart); err == nil {
		tail := make([]byte, window)
		n, _ = io.ReadFull(f, tail)
		if n > 0 {
			h.Write(tail[:n])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
