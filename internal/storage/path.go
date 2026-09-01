package storage

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

var (
	ErrNotAbsolute     = errors.New("storage path must be absolute")
	ErrForbiddenPath   = errors.New("storage path is not allowed")
	ErrPathTraversal   = errors.New("storage path traversal is not allowed")
	ErrSymlinkEscape   = errors.New("storage path symlink escapes the allowed root")
	ErrOverlap         = errors.New("storage path overlaps another No-dal pool")
	ErrNotDirectory    = errors.New("storage path is not a directory")
	ErrNotWritable     = errors.New("storage path is not writable")
	ErrPoolUnavailable = errors.New("storage pool is unavailable")
	ErrBackingChanged  = errors.New("storage pool backing filesystem changed")
	ErrCapacity        = errors.New("insufficient usable storage capacity")
	ErrInvalidSize     = errors.New("invalid volume size")
	ErrUnsupportedFmt  = errors.New("unsupported volume format")
	ErrUnsupportedCls  = errors.New("unsupported storage class")
	ErrDuplicate       = errors.New("object already exists")
	ErrInvalidUpload   = errors.New("invalid upload")
)

var forbiddenExact = []string{
	"/",
}

var forbiddenPrefixes = []string{
	"/etc",
	"/usr",
	"/bin",
	"/sbin",
	"/lib",
	"/lib32",
	"/lib64",
	"/libx32",
	"/boot",
	"/proc",
	"/sys",
	"/dev",
	"/run",
	"/root",
	"/tmp",
	"/var/run",
	"/var/lock",
	"/var/lib/postgresql",
	"/var/log",
	"/var/cache",
	"/etc/ndl",
}

const (
	ndlStateDir   = "/var/lib/ndl"
	ndlStorageDir = "/var/lib/ndl/storage"
)

// Normalize cleans a POSIX host path. It rejects relative paths and ".." tricks.
func Normalize(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	p = strings.ReplaceAll(p, `\`, "/")
	if p == "" || !strings.HasPrefix(p, "/") {
		return "", ErrNotAbsolute
	}
	if strings.Contains(p, "\x00") {
		return "", ErrForbiddenPath
	}
	if strings.Contains(p, "..") {
		return "", fmt.Errorf("%w: %s", ErrPathTraversal, p)
	}
	cleaned := path.Clean(p)
	if cleaned == "." || !strings.HasPrefix(cleaned, "/") {
		return "", ErrNotAbsolute
	}
	return cleaned, nil
}

// Forbidden reports whether a cleaned absolute path is an unsafe pool root.
func Forbidden(cleaned string) bool {
	if cleaned == "" {
		return true
	}
	for _, exact := range forbiddenExact {
		if cleaned == exact {
			return true
		}
	}
	for _, prefix := range forbiddenPrefixes {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return true
		}
	}
	if cleaned == ndlStateDir {
		return true
	}
	if strings.HasPrefix(cleaned, ndlStateDir+"/") && !underStorage(cleaned) {
		return true
	}
	return false
}

func underStorage(cleaned string) bool {
	return cleaned == ndlStorageDir || strings.HasPrefix(cleaned, ndlStorageDir+"/")
}

// Overlaps reports whether two cleaned directory paths nest or are equal.
func Overlaps(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// JoinUnder joins a cleaned root with a relative locator and rejects escape.
func JoinUnder(root, rel string) (string, error) {
	root, err := Normalize(root)
	if err != nil {
		return "", err
	}
	rel = strings.TrimSpace(strings.ReplaceAll(rel, `\`, "/"))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || rel == "." {
		return "", ErrPathTraversal
	}
	joined := path.Clean(root + "/" + rel)
	if joined == root || !strings.HasPrefix(joined, root+"/") {
		return "", ErrPathTraversal
	}
	return joined, nil
}

// RelUnder returns the locator of abs beneath root.
func RelUnder(root, abs string) (string, error) {
	root, err := Normalize(root)
	if err != nil {
		return "", err
	}
	abs, err = Normalize(abs)
	if err != nil {
		return "", err
	}
	if abs == root {
		return "", ErrPathTraversal
	}
	if !strings.HasPrefix(abs, root+"/") {
		return "", ErrPathTraversal
	}
	return strings.TrimPrefix(abs, root+"/"), nil
}

// DisplayName sanitizes an upload filename for metadata only.
func DisplayName(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, `\`, "/")
	s = path.Base(s)
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == ".." {
		return "upload"
	}
	var b strings.Builder
	for _, r := range s {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "upload"
	}
	return out
}

// ValidClass reports a known storage class.
func ValidClass(class string) bool {
	switch class {
	case ClassVMDisk, ClassContainerRoot, ClassISO, ClassTemplate, ClassBackupStaging:
		return true
	default:
		return false
	}
}

// ValidLibraryKind reports a known library kind.
func ValidLibraryKind(kind string) bool {
	return kind == LibraryISO || kind == LibraryCloudImage || kind == LibraryDiskImage
}

func classKindFormat(class, format string) (kind, resolved string, err error) {
	if !ValidClass(class) {
		return "", "", ErrUnsupportedCls
	}
	switch class {
	case ClassVMDisk, ClassTemplate:
		if format == "" {
			format = FormatQCOW2
		}
		if format != FormatQCOW2 && format != FormatRaw {
			return "", "", ErrUnsupportedFmt
		}
		return KindBlock, format, nil
	case ClassContainerRoot, ClassBackupStaging:
		if format == "" || format == FormatDirectory {
			return KindFilesystem, FormatDirectory, nil
		}
		return "", "", ErrUnsupportedFmt
	case ClassISO:
		return "", "", fmt.Errorf("%w: use the image library to store ISO media", ErrUnsupportedCls)
	default:
		return "", "", ErrUnsupportedCls
	}
}
