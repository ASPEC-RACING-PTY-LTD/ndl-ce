package storecatalog

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appmanifest"
)

//go:embed official.pub official/*.yaml official/*.sig
var officialFS embed.FS

// File is one bundled Official manifest plus its publisher signature.
type File struct {
	Path      string
	YAML      string
	Signature string
	Manifest  appmanifest.Manifest
}

// OfficialPublicKey is the pinned Official publisher public key (base64
// Ed25519). Clusters verify Official packages against this pin and never
// generate an Official-class private key.
func OfficialPublicKey() string {
	raw, err := officialFS.ReadFile("official.pub")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
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
		sig := ""
		if b, err := officialFS.ReadFile(path + ".sig"); err == nil {
			sig = strings.TrimSpace(string(b))
		}
		out = append(out, File{Path: path, YAML: string(raw), Signature: sig, Manifest: *m})
		return nil
	})
	return out, err
}
