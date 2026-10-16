// Command ndl-live publishes and applies NDL-CE live updates.
//
// On a build machine:
//
//	ndl-live keygen <dir>                       make a signing key pair
//	ndl-live pack -key K -out F -version V ...  sign a bundle
//
// On a host (as root):
//
//	ndl-live trust <key.pub>    trust a signing key
//	ndl-live enable|disable     turn automatic application on or off
//	ndl-live run                apply what is queued (the systemd service)
//	ndl-live install <bundle>   queue a bundle
//	ndl-live rollback           undo the newest live update
//	ndl-live status             show state
//	ndl-live package-installed  record that a package replaced live updates
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/liveupdate"
)

const (
	httpSocketUnit = "ndl-control-http.socket"
	httpDropIn     = "/etc/systemd/system/ndl-control.service.d/ndl-live-http-socket.conf"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "pack":
		err = pack(os.Args[2:])
	case "trust":
		err = trust(os.Args[2:])
	case "enable":
		err = enable(os.Args[2:])
	case "disable":
		err = disable()
	case "run":
		err = run()
	case "install":
		err = install(os.Args[2:])
	case "rollback":
		err = queueRollback()
	case "status":
		err = status()
	case "package-installed":
		v := ""
		if len(os.Args) > 2 {
			v = os.Args[2]
		}
		err = updater(nil).PackageInstalled(v)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ndl-live:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ndl-live keygen|pack|trust|enable|disable|run|install|rollback|status|package-installed")
}

func root() string {
	if r := os.Getenv("NDL_LIVE_ROOT"); r != "" {
		return r
	}
	return liveupdate.DefaultRoot
}

func keysDir() string {
	if d := os.Getenv("NDL_LIVE_KEYS"); d != "" {
		return d
	}
	return liveupdate.DefaultKeysDir
}

func updater(keys liveupdate.Keys) *liveupdate.Updater {
	return &liveupdate.Updater{Root: root(), BinDir: "/usr/sbin", Keys: keys, Sys: liveupdate.Host{}}
}

func keygen(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: ndl-live keygen <dir>")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(args[0], 0o700); err != nil {
		return err
	}
	keyPath := filepath.Join(args[0], "ndl-live-signing.key")
	if _, err := os.Stat(keyPath); err == nil {
		return fmt.Errorf("%s exists; refusing to replace a signing key", keyPath)
	}
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return err
	}
	pubPath := filepath.Join(args[0], "ndl-live-signing.pub")
	if err := os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("signing key %s written to %s\ntrust it on a host with: ndl-live trust %s\n", liveupdate.KeyID(pub), keyPath, pubPath)
	return nil
}

func pack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	keyPath := fs.String("key", "", "signing key file")
	out := fs.String("out", "", "bundle to write")
	version := fs.String("version", "", "version shown to people, for example 1.5.0+live.3")
	base := fs.String("base", "", "package version it builds on, for example 1.5.0")
	id := fs.String("id", "", "update ID (default: time and version)")
	src := fs.String("src", "", "directory holding ui/ and bin/ to install")
	migrations := fs.String("migrations", "", "migrations directory the control binary embeds")
	allowMigrations := fs.Bool("allow-migrations", false, "let the control binary apply new migrations")
	notes := fs.String("notes", "", "what changed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || *out == "" || *version == "" || *base == "" || *src == "" {
		return errors.New("pack needs -key, -out, -version, -base and -src")
	}
	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	key, err := liveupdate.ParsePrivateKey(string(raw))
	if err != nil {
		return err
	}
	m := liveupdate.Manifest{ID: *id, Version: *version, BaseVersion: *base, Notes: *notes, AllowMigrations: *allowMigrations}
	if m.ID == "" {
		m.ID = time.Now().UTC().Format("20060102-150405") + "-" + strings.NewReplacer("+", "-", " ", "-").Replace(*version)
	}
	if *migrations != "" {
		entries, err := os.ReadDir(*migrations)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
				m.Migrations = append(m.Migrations, e.Name())
			}
		}
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	m, err = liveupdate.Pack(f, m, *src, key)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(*out)
		return err
	}
	fmt.Printf("%s %s: %s (%d files)\n", m.ID, m.Version, strings.Join(m.Components(), ", "), len(m.Files))
	return nil
}

func requireRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("run as root")
	}
	return nil
}

func trust(args []string) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if len(args) != 1 {
		return errors.New("usage: ndl-live trust <key.pub>")
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	pub, err := liveupdate.ParsePublicKey(string(raw))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(keysDir(), 0o755); err != nil {
		return err
	}
	id := liveupdate.KeyID(pub)
	if err := os.WriteFile(filepath.Join(keysDir(), id+".pub"), []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Println("trusted signing key", id)
	return nil
}

func controlGID() int {
	g, err := user.LookupGroup("ndl-control")
	if err != nil {
		return -1
	}
	gid, _ := strconv.Atoi(g.Gid)
	return gid
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// enable turns live updates on. With -http-socket the web port is also
// held by systemd, so restarting the control plane queues browser requests
// instead of refusing them; that needs one control-plane restart.
func enable(args []string) error {
	if err := requireRoot(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("enable", flag.ContinueOnError)
	httpSocket := fs.Bool("http-socket", false, "let systemd hold the web port across restarts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	keys, err := liveupdate.LoadKeys(keysDir())
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return errors.New("trust a signing key first: ndl-live trust <key.pub>")
	}
	if err := updater(keys).Prepare(controlGID()); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(liveupdate.DefaultEnabledMarker), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(liveupdate.DefaultEnabledMarker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return err
	}
	if err := systemctl("enable", "--now", "ndl-live.path", "ndl-live.timer"); err != nil {
		return err
	}
	if *httpSocket {
		if err := os.MkdirAll(filepath.Dir(httpDropIn), 0o755); err != nil {
			return err
		}
		conf := "[Unit]\nRequires=" + httpSocketUnit + "\nAfter=" + httpSocketUnit + "\n\n[Service]\nSockets=ndl-control.socket " + httpSocketUnit + "\n"
		if err := os.WriteFile(httpDropIn, []byte(conf), 0o644); err != nil {
			return err
		}
		// The running control plane holds the port; stop it so systemd can.
		if err := systemctl("daemon-reload"); err != nil {
			return err
		}
		if err := systemctl("stop", "ndl-control.service"); err != nil {
			return err
		}
		if err := systemctl("enable", "--now", httpSocketUnit); err != nil {
			_ = os.Remove(httpDropIn)
			_ = systemctl("daemon-reload")
			_ = systemctl("start", "ndl-control.service")
			return err
		}
		if err := systemctl("start", "ndl-control.service"); err != nil {
			return err
		}
	}
	fmt.Println("live updates are on")
	return nil
}

func disable() error {
	if err := requireRoot(); err != nil {
		return err
	}
	_ = os.Remove(liveupdate.DefaultEnabledMarker)
	if err := systemctl("disable", "--now", "ndl-live.path", "ndl-live.timer"); err != nil {
		return err
	}
	fmt.Println("live updates are off; the running version stays. Undo the last one with ndl-live rollback.")
	return nil
}

func enabled() bool {
	_, err := os.Stat(liveupdate.DefaultEnabledMarker)
	return err == nil
}

func run() error {
	if err := requireRoot(); err != nil {
		return err
	}
	keys, err := liveupdate.LoadKeys(keysDir())
	if err != nil {
		return err
	}
	u := updater(keys)
	ctx := context.Background()
	if !enabled() {
		// Still undo an interrupted update so a host is never left half
		// updated, but apply nothing new.
		return u.Recover(ctx)
	}
	return u.Run(ctx)
}

func install(args []string) error {
	if err := requireRoot(); err != nil {
		return err
	}
	if len(args) != 1 {
		return errors.New("usage: ndl-live install <bundle>")
	}
	keys, err := liveupdate.LoadKeys(keysDir())
	if err != nil {
		return err
	}
	in, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer in.Close()
	m, err := liveupdate.Inspect(in, keys)
	if err != nil {
		return err
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	dst := filepath.Join(root(), "inbox", m.ID+liveupdate.BundleExt)
	tmp := dst + ".partial"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("%v (run ndl-live enable first)", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	if enabled() {
		fmt.Printf("queued %s %s; it applies as soon as nothing is running\n", m.ID, m.Version)
	} else {
		fmt.Printf("queued %s %s; live updates are off, so it waits until ndl-live enable\n", m.ID, m.Version)
	}
	return nil
}

func queueRollback() error {
	if err := requireRoot(); err != nil {
		return err
	}
	keys, _ := liveupdate.LoadKeys(keysDir())
	return updater(keys).Rollback(context.Background())
}

func status() error {
	st, err := liveupdate.ReadState(root())
	if err != nil {
		return err
	}
	out := map[string]any{"enabled": enabled(), "state": st}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
