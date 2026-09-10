//go:build !linux

package lxc

import "os"

func withCreateUmask(_ int, fn func() error) error {
	return fn()
}

func sanitizeRootfs(rootfs string, spec Spec) error {
	_ = spec
	for _, rel := range []string{".", "etc"} {
		p := rootfs
		if rel != "." {
			p = rootfs + string(os.PathSeparator) + rel
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.Mode()&0o111 != 0o111 {
			_ = os.Chmod(p, st.Mode()|0o111)
		}
	}
	return nil
}
