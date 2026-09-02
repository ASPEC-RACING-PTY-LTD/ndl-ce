package storecatalog

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appmanifest"
)

//go:embed official/*.yaml
var officialFS embed.FS

// File is one bundled Official manifest.
type File struct {
	Path     string
	YAML     string
	Manifest appmanifest.Manifest
}

// Official returns bundled Official-class manifests.
func Official() ([]File, error) {
	var out []File
	err := fs.WalkDir(officialFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		raw, err := officialFS.ReadFile(path)
		if err != nil {
			return err
		}
		m, err := appmanifest.ParseYAML(raw)
		if err != nil {
			return err
		}
		out = append(out, File{Path: path, YAML: string(raw), Manifest: *m})
		return nil
	})
	return out, err
}
