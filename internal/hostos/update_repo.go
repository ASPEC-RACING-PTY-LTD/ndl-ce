package hostos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// The signed No-dal APT repository. These match packaging/bootstrap/get-nodal.sh
// and are fixed: the update surface never takes a repository or key from a
// request.
const (
	RepositoryURL    = "https://packages.no-dal.com/debian"
	RepositoryKeyURL = "https://packages.no-dal.com/gpg"
	repositorySuite  = "trixie"
	repositorySource = "etc/apt/sources.list.d/nodal.sources"
	repositoryList   = "etc/apt/sources.list.d/nodal.list"
	keyringArmored   = "usr/share/keyrings/nodal.asc"
	keyringBinary    = "usr/share/keyrings/nodal.gpg"
	maxKeyBytes      = 64 << 10
)

// RepositoryNotConfigured is the honest reason a check cannot see releases.
const RepositoryNotConfigured = "The signed No-dal release repository is not configured on this host, so new releases cannot be seen. Enable it on the Updates page."

// repoRoot and fetchRepositoryKey are replaced in tests.
var (
	repoRoot           = "/"
	fetchRepositoryKey = httpFetchKey
)

// RepositoryConfigured reports whether an APT source for the No-dal
// repository exists (from get-nodal.sh, the Updates page, or by hand).
func RepositoryConfigured() bool {
	for _, rel := range []string{repositorySource, repositoryList} {
		if st, err := os.Stat(filepath.Join(repoRoot, rel)); err == nil && st.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func httpFetchKey(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, RepositoryKeyURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("signing key download returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxKeyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxKeyBytes {
		return nil, errors.New("signing key is larger than expected")
	}
	return body, nil
}

// keyringName picks the keyring file for key, or "" when key is not an
// OpenPGP public key. APT reads armored keys from .asc and binary keys
// from .gpg.
func keyringName(key []byte) string {
	trimmed := bytes.TrimSpace(key)
	if bytes.HasPrefix(trimmed, []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----")) {
		return keyringArmored
	}
	// Binary public-key packet: old format tag 6 (0x98-0x9b) or new format (0xc6).
	if len(key) > 0 && (key[0]&0xfc == 0x98 || key[0] == 0xc6) {
		return keyringBinary
	}
	return ""
}

func repositorySources(keyring string) string {
	return "Types: deb\nURIs: " + RepositoryURL + "\nSuites: " + repositorySuite +
		"\nComponents: main\nSigned-By: /" + keyring + "\n"
}

func writeRepoFile(rel string, data []byte) error {
	path := filepath.Join(repoRoot, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".ndl-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// enableRepository installs the signing key and APT source exactly as
// get-nodal.sh does. An existing source is left alone.
func enableRepository(ctx context.Context, res UpdateResult, dryRun bool) (UpdateResult, error) {
	res.DryRun = dryRun
	if RepositoryConfigured() {
		res.RepositoryConfigured = true
		res.Reason = "The release repository is already configured."
		return res, nil
	}
	if dryRun {
		res.Status = "planned"
		res.Reason = "The signing key would be downloaded from " + RepositoryKeyURL + " and " + RepositoryURL + " added as an APT source."
		return res, nil
	}
	key, err := fetchRepositoryKey(ctx)
	if err != nil {
		res.Status = "failed"
		res.Reason = "could not download the repository signing key: " + err.Error()
		return res, nil
	}
	keyring := keyringName(key)
	if keyring == "" {
		res.Status = "failed"
		res.Reason = "the downloaded file is not an OpenPGP public key"
		return res, nil
	}
	if err := writeRepoFile(keyring, key); err != nil {
		res.Status = "failed"
		res.Reason = "could not write the repository signing key"
		return res, nil
	}
	if err := writeRepoFile(repositorySource, []byte(repositorySources(keyring))); err != nil {
		res.Status = "failed"
		res.Reason = "could not write the repository source"
		return res, nil
	}
	res.RepositoryConfigured = true
	res.Reason = "The signed release repository is configured."
	return res, nil
}
