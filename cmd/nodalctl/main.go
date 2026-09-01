package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/install"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "nodalctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Print(`nodalctl commands:
  version
  setup --token TOKEN --username USER --password PASS
  login --username USER --password PASS
  whoami
  token create --name NAME
  recover-admin --username USER --password PASS
  host-prepare
  node show
  task list
  event list
  storage pool list
  storage pool create --name NAME --path PATH
  storage volume list
  storage volume create --pool-id ID --class CLASS --size-bytes N
  storage image list
  storage image upload --pool-id ID --kind KIND --file PATH
`)
		return nil
	}
	switch args[0] {
	case "version", "--version":
		fmt.Println("nodalctl 0.1.0")
		return nil
	case "setup":
		return cmdSetup(args[1:])
	case "login":
		return cmdLogin(args[1:])
	case "whoami":
		return cmdWhoami()
	case "token":
		if len(args) < 2 || args[1] != "create" {
			return fmt.Errorf("usage: nodalctl token create --name NAME")
		}
		return cmdTokenCreate(args[2:])
	case "recover-admin":
		return cmdRecover(args[1:])
	case "host-prepare":
		return install.HostPrepare()
	case "node":
		if len(args) < 2 || args[1] != "show" {
			return fmt.Errorf("usage: nodalctl node show")
		}
		return cmdGet("/api/v1/nodes")
	case "task":
		if len(args) < 2 || args[1] != "list" {
			return fmt.Errorf("usage: nodalctl task list")
		}
		return cmdGet("/api/v1/tasks")
	case "event":
		if len(args) < 2 || args[1] != "list" {
			return fmt.Errorf("usage: nodalctl event list")
		}
		return cmdGet("/api/v1/events")
	case "storage":
		return cmdStorage(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func baseURL() string {
	if u := os.Getenv("NODAL_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://127.0.0.1:8080"
}

func cmdSetup(args []string) error {
	f := parseFlags(args)
	token := f["token"]
	if token == "" {
		if b, err := os.ReadFile("/var/lib/ndl/setup.token"); err == nil {
			token = strings.TrimSpace(string(b))
		}
	}
	return postJSON("/api/v1/setup/claim", map[string]string{
		"token":    token,
		"username": f["username"],
		"password": f["password"],
	}, true)
}

func cmdLogin(args []string) error {
	f := parseFlags(args)
	return postJSON("/api/v1/auth/login", map[string]string{
		"username": f["username"],
		"password": f["password"],
	}, true)
}

func cmdWhoami() error {
	resp, err := do("GET", "/api/v1/me", nil, true)
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func cmdTokenCreate(args []string) error {
	f := parseFlags(args)
	resp, err := do("POST", "/api/v1/tokens", map[string]string{"name": f["name"]}, true)
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func cmdGet(path string) error {
	resp, err := do("GET", path, nil, true)
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func cmdRecover(args []string) error {
	f := parseFlags(args)
	return install.RecoverAdmin(f["username"], f["password"])
}

func postJSON(path string, body any, saveSession bool) error {
	resp, err := do("POST", path, body, saveSession)
	if err != nil {
		return err
	}
	if len(resp) > 0 {
		fmt.Println(string(resp))
	}
	return nil
}

func do(method, path string, body any, useSession bool) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, baseURL()+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if useSession {
		if c, err := os.ReadFile(sessionFile()); err == nil {
			req.Header.Set("Cookie", strings.TrimSpace(string(c)))
		}
		if tok := os.Getenv("NODAL_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if useSession {
		for _, c := range res.Cookies() {
			if c.Name == "ndl_session" {
				_ = os.MkdirAll(filepath.Dir(sessionFile()), 0700)
				_ = os.WriteFile(sessionFile(), []byte(c.Name+"="+c.Value), 0600)
			}
		}
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(b)))
	}
	return b, nil
}

func sessionFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "nodal", "session")
}

func cmdStorage(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: nodalctl storage pool|volume|image ...")
	}
	switch args[0] + " " + args[1] {
	case "pool list":
		return cmdGet("/api/v1/storage/pools")
	case "pool create":
		f := parseFlags(args[2:])
		body := map[string]any{"name": f["name"], "path": f["path"], "create": true}
		return postJSON("/api/v1/storage/pools", body, true)
	case "volume list":
		return cmdGet("/api/v1/storage/volumes")
	case "volume create":
		f := parseFlags(args[2:])
		var size int64
		fmt.Sscan(f["size-bytes"], &size)
		return postJSON("/api/v1/storage/volumes", map[string]any{
			"pool_id": f["pool-id"], "class": f["class"], "size_bytes": size, "format": f["format"],
		}, true)
	case "image list":
		return cmdGet("/api/v1/storage/images")
	case "image upload":
		f := parseFlags(args[2:])
		return cmdUploadImage(f["pool-id"], f["kind"], f["file"])
	default:
		return fmt.Errorf("unknown storage command")
	}
}

func cmdUploadImage(poolID, kind, file string) error {
	if poolID == "" || kind == "" || file == "" {
		return fmt.Errorf("usage: nodalctl storage image upload --pool-id ID --kind KIND --file PATH")
	}
	fh, err := os.Open(file)
	if err != nil {
		return err
	}
	defer fh.Close()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		_ = mw.WriteField("pool_id", poolID)
		_ = mw.WriteField("kind", kind)
		_ = mw.WriteField("filename", filepath.Base(file))
		part, err := mw.CreateFormFile("file", filepath.Base(file))
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, fh); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = mw.Close()
		_ = pw.Close()
	}()
	req, err := http.NewRequest("POST", baseURL()+"/api/v1/storage/images", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c, err := os.ReadFile(sessionFile()); err == nil {
		req.Header.Set("Cookie", strings.TrimSpace(string(c)))
	}
	if tok := os.Getenv("NODAL_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(b)))
	}
	fmt.Println(string(b))
	return nil
}

func parseFlags(args []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			continue
		}
		key := strings.TrimPrefix(args[i], "--")
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			out[key] = args[i+1]
			i++
		} else {
			out[key] = "true"
		}
	}
	return out
}
