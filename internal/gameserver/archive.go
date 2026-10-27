package gameserver

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Addon folders (Garry's Mod, Rust plugins) hold many small files.
	maxArchiveFiles = 20000
	maxArchiveBytes = 512 << 20
	maxOneFileBytes = 128 << 20
)

// Archive limits, for building one from the file manager too.
const (
	MaxArchiveFiles = maxArchiveFiles
	MaxArchiveBytes = maxArchiveBytes
	MaxOneFileBytes = maxOneFileBytes
)

// IsArchive reports whether SafeExtract unpacks a file of this name.
func IsArchive(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".zip", ".tar.gz", ".tgz", ".tar"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// ZipFile is one entry for BuildZip. Name uses forward slashes; a folder
// has Dir set and no Body.
type ZipFile struct {
	Name     string
	Dir      bool
	Body     []byte
	Modified time.Time
}

// BuildZip writes files into a deflated zip archive.
func BuildZip(files []ZipFile) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		name := strings.TrimPrefix(f.Name, "/")
		if name == "" || strings.Contains("/"+name+"/", "/../") {
			return nil, fmt.Errorf("archive entry %q is not valid", f.Name)
		}
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: f.Modified}
		if f.Dir {
			hdr.Name = strings.TrimSuffix(name, "/") + "/"
			hdr.Method = zip.Store
			hdr.SetMode(0o750 | os.ModeDir)
		} else {
			hdr.SetMode(0o640)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, err
		}
		if !f.Dir {
			if _, err := w.Write(f.Body); err != nil {
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func SafeExtract(root, destRel string, raw []byte, name string) error {
	destRel = strings.TrimSpace(destRel)
	if destRel == "" {
		destRel = "."
	}
	if destRel == ".." || strings.HasPrefix(destRel, "../") {
		return fmt.Errorf("extract path escapes the jail")
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(root, destRel, raw)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractTar(root, destRel, raw, true)
	case strings.HasSuffix(lower, ".tar"):
		return extractTar(root, destRel, raw, false)
	default:
		return writeJailedFile(root, filepath.ToSlash(filepath.Join(destRel, pathBase(name))), raw)
	}
}

func writeJailedFile(root, rel string, raw []byte) error {
	rel, err := cleanArchiveRel(rel)
	if err != nil {
		return err
	}
	abs, err := joinArchive(root, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return err
	}
	return os.WriteFile(abs, raw, 0o640)
}

func makeJailedDir(root, rel string) error {
	rel, err := cleanArchiveRel(strings.TrimSuffix(rel, "/"))
	if err != nil {
		return err
	}
	abs, err := joinArchive(root, rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o750)
}

func extractZip(root, destRel string, raw []byte) error {
	r, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return fmt.Errorf("zip is not valid")
	}
	var total int64
	if len(r.File) > maxArchiveFiles {
		return fmt.Errorf("archive has too many files")
	}
	for _, f := range r.File {
		if strings.HasSuffix(f.Name, "/") {
			// Empty folders are kept, as they were zipped.
			if err := makeJailedDir(root, filepath.ToSlash(filepath.Join(destRel, f.Name))); err != nil {
				return err
			}
			continue
		}
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			continue
		}
		rel, err := cleanArchiveRel(filepath.ToSlash(filepath.Join(destRel, f.Name)))
		if err != nil {
			return err
		}
		if f.UncompressedSize64 > maxOneFileBytes {
			return fmt.Errorf("archive file is too large")
		}
		total += int64(f.UncompressedSize64)
		if total > maxArchiveBytes {
			return fmt.Errorf("archive is larger than the allowed extract size")
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxOneFileBytes+1))
		_ = rc.Close()
		if err != nil {
			return err
		}
		if int64(len(body)) > maxOneFileBytes {
			return fmt.Errorf("archive file is too large")
		}
		if err := writeJailedFile(root, rel, body); err != nil {
			return err
		}
	}
	return nil
}

func extractTar(root, destRel string, raw []byte, gzipped bool) error {
	var r io.Reader = bytes.NewReader(raw)
	if gzipped {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("gzip is not valid")
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	var files int
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar is not valid")
		}
		if hdr.Typeflag == tar.TypeDir {
			if err := makeJailedDir(root, filepath.ToSlash(filepath.Join(destRel, hdr.Name))); err != nil {
				return err
			}
			continue
		}
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			continue
		}
		files++
		if files > maxArchiveFiles {
			return fmt.Errorf("archive has too many files")
		}
		if hdr.Size > maxOneFileBytes {
			return fmt.Errorf("archive file is too large")
		}
		total += hdr.Size
		if total > maxArchiveBytes {
			return fmt.Errorf("archive is larger than the allowed extract size")
		}
		rel, err := cleanArchiveRel(filepath.ToSlash(filepath.Join(destRel, hdr.Name)))
		if err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(tr, maxOneFileBytes+1))
		if err != nil {
			return err
		}
		if int64(len(body)) > maxOneFileBytes {
			return fmt.Errorf("archive file is too large")
		}
		if err := writeJailedFile(root, rel, body); err != nil {
			return err
		}
	}
}

func cleanArchiveRel(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "" || rel == "." {
		return ".", nil
	}
	rel = strings.TrimPrefix(rel, "/")
	if strings.Contains(rel, "\x00") {
		return "", fmt.Errorf("path contains a NUL")
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes the jail")
	}
	return clean, nil
}

func joinArchive(root, rel string) (string, error) {
	rel, err := cleanArchiveRel(rel)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	if rel == "." {
		return root, nil
	}
	out := filepath.Join(root, filepath.FromSlash(rel))
	relOut, err := filepath.Rel(root, out)
	if err != nil || strings.HasPrefix(relOut, "..") {
		return "", fmt.Errorf("path escapes the jail")
	}
	return out, nil
}
