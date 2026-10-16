package liveupdate

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Inspect verifies a bundle as it streams past without writing anything:
// the signed manifest must come first, as Pack writes it, and every file
// must match its checksum. The control plane uses it to refuse a bad
// upload before queueing it; the updater verifies again on its own.
func Inspect(r io.Reader, keys Keys) (Manifest, error) {
	var m Manifest
	gz, err := gzip.NewReader(io.LimitReader(r, maxBundleBytes+maxMetaBytes))
	if err != nil {
		return m, errors.New("not a live update bundle")
	}
	tr := tar.NewReader(gz)
	var manRaw, sigRaw []byte
	want := map[string]File{}
	seen := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, fmt.Errorf("bundle is damaged: %w", err)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return m, fmt.Errorf("bundle entry %q is not a regular file", h.Name)
		}
		switch h.Name {
		case "manifest.json":
			if manRaw, err = io.ReadAll(io.LimitReader(tr, maxMetaBytes)); err != nil {
				return m, err
			}
		case "manifest.sig":
			if sigRaw, err = io.ReadAll(io.LimitReader(tr, maxMetaBytes)); err != nil {
				return m, err
			}
			if manRaw == nil {
				return m, errors.New("bundle manifest must come first")
			}
			if m, err = VerifyManifest(manRaw, sigRaw, keys); err != nil {
				return m, err
			}
			for _, f := range m.Files {
				want[f.Path] = f
			}
		default:
			if sigRaw == nil {
				return m, errors.New("bundle manifest must come first")
			}
			rel, _ := strings.CutPrefix(h.Name, "files/")
			f, ok := want[rel]
			if !ok || !strings.HasPrefix(h.Name, "files/") {
				return m, fmt.Errorf("bundle entry %q is not in its manifest", h.Name)
			}
			delete(want, rel)
			sum := sha256.New()
			n, err := io.Copy(sum, io.LimitReader(tr, f.Size+1))
			if err != nil {
				return m, err
			}
			if n != f.Size || hex.EncodeToString(sum.Sum(nil)) != f.SHA256 {
				return m, fmt.Errorf("bundle file %q does not match its checksum", rel)
			}
			seen++
		}
	}
	if sigRaw == nil {
		return m, errors.New("bundle has no signed manifest")
	}
	if len(want) > 0 {
		return m, errors.New("bundle is missing files its manifest lists")
	}
	return m, nil
}
