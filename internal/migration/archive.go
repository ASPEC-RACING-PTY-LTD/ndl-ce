package migration

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

const (
	defaultMaxMemberBytes = 512 << 30
	defaultMaxTotalBytes  = 1 << 40
	maxNameLen            = 4096
)

// ExtractTar writes regular files, directories, and relative symlinks from r
// into dest. Absolute names, traversal, device nodes, and escaped symlinks are
// refused. Hard links may only target files already written under dest.
func ExtractTar(r io.Reader, dest string, maxTotal int64) error {
	if maxTotal <= 0 {
		maxTotal = defaultMaxTotalBytes
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	br := io.LimitReader(r, maxTotal+1)
	tr := tar.NewReader(br)
	var written int64
	seen := map[string]struct{}{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if len(hdr.Name) > maxNameLen {
			return fmt.Errorf("archive member name is too long")
		}
		target, err := RelJail(dest, hdr.Name)
		if err != nil {
			return fmt.Errorf("malicious archive path: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			_ = os.Chmod(target, os.FileMode(hdr.Mode)&0o755)
			restoreXattrs(target, hdr)
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size < 0 || hdr.Size > defaultMaxMemberBytes {
				return fmt.Errorf("archive member exceeds size limit")
			}
			if written+hdr.Size > maxTotal {
				return fmt.Errorf("archive exceeds decompression limit")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode) & 0o755
			if mode == 0 {
				mode = 0o644
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			n, copyErr := io.CopyN(f, tr, hdr.Size)
			closeErr := f.Close()
			if copyErr != nil && copyErr != io.EOF {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			written += n
			seen[target] = struct{}{}
			if hdr.Mode&0o7000 != 0 {
				_ = os.Chmod(target, os.FileMode(hdr.Mode)&0o7755)
			}
			_ = os.Chtimes(target, hdr.AccessTime, hdr.ModTime)
			restoreXattrs(target, hdr)
		case tar.TypeSymlink:
			link := hdr.Linkname
			if filepath.IsAbs(link) || strings.Contains(filepath.Clean(link), "..") {
				return fmt.Errorf("symlink escape refused")
			}
			resolved := filepath.Join(filepath.Dir(target), link)
			if !strings.HasPrefix(resolved, filepath.Clean(dest)+string(os.PathSeparator)) && resolved != filepath.Clean(dest) {
				return fmt.Errorf("symlink escape refused")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		case tar.TypeLink:
			linkTarget, err := RelJail(dest, hdr.Linkname)
			if err != nil {
				return fmt.Errorf("hard link escape refused")
			}
			if _, ok := seen[linkTarget]; !ok {
				return fmt.Errorf("hard link target is missing")
			}
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader, tar.TypeGNUSparse:
			continue
		default:
			return fmt.Errorf("archive member type %c is refused", hdr.Typeflag)
		}
	}
}

func restoreXattrs(path string, hdr *tar.Header) {
	if hdr == nil {
		return
	}
	for k, v := range hdr.PAXRecords {
		name := ""
		if n, ok := strings.CutPrefix(k, "SCHILY.xattr."); ok {
			name = n
		}
		if n, ok := strings.CutPrefix(k, "LIBARCHIVE.xattr."); ok {
			name = n
		}
		if name == "" || strings.Contains(name, "..") {
			continue
		}
		_ = unix.Setxattr(path, name, []byte(v), 0)
	}
}

func MaybeDecompress(r io.Reader, name string) (io.Reader, error) {
	low := strings.ToLower(name)
	if strings.HasSuffix(low, ".gz") || strings.HasSuffix(low, ".tgz") {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, err
		}
		return gz, nil
	}
	if strings.HasSuffix(low, ".zst") || strings.HasSuffix(low, ".zstd") {
		dec, err := zstd.NewReader(r)
		if err != nil {
			return nil, err
		}
		return dec, nil
	}
	return r, nil
}

func ExtractArchiveFile(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := MaybeDecompress(f, src)
	if err != nil {
		return err
	}
	if c, ok := r.(io.Closer); ok && r != f {
		defer c.Close()
	}
	return ExtractTar(r, dest, 0)
}

// WriteTar archives dir into dest as an uncompressed tar. Device nodes are refused.
func WriteTar(dir, dest string) error {
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)
	err = filepath.Walk(dir, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.Contains(rel, "..") {
			return fmt.Errorf("path traversal refused")
		}
		mode := info.Mode()
		if mode&os.ModeDevice != 0 || mode&os.ModeNamedPipe != 0 || mode&os.ModeSocket != 0 {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) || strings.Contains(filepath.Clean(link), "..") {
				return fmt.Errorf("symlink escape refused")
			}
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = link
			return tw.WriteHeader(hdr)
		}
		if sz, err := unix.Llistxattr(p, nil); err == nil && sz > 0 {
			names := make([]byte, sz)
			n, err := unix.Llistxattr(p, names)
			if err == nil && n > 0 {
				if hdr.PAXRecords == nil {
					hdr.PAXRecords = map[string]string{}
				}
				for _, nm := range splitXattrNames(names[:n]) {
					if nm == "" {
						continue
					}
					vsz, err := unix.Lgetxattr(p, nm, nil)
					if err != nil || vsz <= 0 {
						continue
					}
					val := make([]byte, vsz)
					got, err := unix.Lgetxattr(p, nm, val)
					if err != nil || got <= 0 {
						continue
					}
					hdr.PAXRecords["SCHILY.xattr."+nm] = string(val[:got])
				}
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	closeTw := tw.Close()
	closeF := f.Close()
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if closeTw != nil {
		_ = os.Remove(dest)
		return closeTw
	}
	if closeF != nil {
		_ = os.Remove(dest)
		return closeF
	}
	return nil
}

func splitXattrNames(b []byte) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c == 0 {
			if i > start {
				out = append(out, string(b[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}
