package iojail

import (
	"bytes"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	// EditorMaxBytes is the largest payload the built-in editor will write.
	EditorMaxBytes = 1 << 20
	// PreviewMaxBytes is the largest payload returned as text preview.
	PreviewMaxBytes = 2 << 20
	binarySniff     = 8192
)

var binaryExt = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".ico": {},
	".bmp": {}, ".tif": {}, ".tiff": {}, ".pdf": {}, ".zip": {}, ".gz": {},
	".tgz": {}, ".bz2": {}, ".xz": {}, ".7z": {}, ".rar": {}, ".tar": {},
	".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {}, ".eot": {},
	".exe": {}, ".dll": {}, ".so": {}, ".dylib": {}, ".o": {}, ".a": {},
	".wasm": {}, ".mp3": {}, ".mp4": {}, ".mkv": {}, ".mov": {}, ".avi": {},
	".iso": {}, ".img": {}, ".qcow2": {}, ".vmdk": {}, ".bin": {}, ".dat": {},
}

// LooksBinary reports whether name or the sniffed prefix should not be
// opened as text.
func LooksBinary(name string, prefix []byte) bool {
	ext := strings.ToLower(path.Ext(name))
	if _, ok := binaryExt[ext]; ok {
		return true
	}
	n := len(prefix)
	if n > binarySniff {
		prefix = prefix[:binarySniff]
		n = binarySniff
	}
	if n == 0 {
		return false
	}
	if bytes.IndexByte(prefix, 0) >= 0 {
		return true
	}
	if !utf8.Valid(prefix) {
		return true
	}
	return false
}
