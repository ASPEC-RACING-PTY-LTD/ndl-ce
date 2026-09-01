package main

import (
	"bytes"
	"crypto/tls"
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
  network list
  network create --name NAME --kind KIND [--cidr CIDR] [--uplink IF] [--dry-run] [--confirm-ifname IF]
  network apply --id ID [--dry-run] [--confirm-ifname IF]
  workload list
  workload create --kind system-container --name NAME --image-pin PIN --pool-id ID --network-id ID [--cpus N] [--memory-bytes N] [--privileged]
  workload create --kind vm --name NAME --network-id ID [--pool-id ID] [--cpus N] [--memory-bytes N] [--firmware bios|uefi] [--cloud-image-id ID] [--iso-library-id ID] [--autostart] [--nocloud-user USER] [--nocloud-host HOST]
  workload get --id ID
  workload start --id ID
  workload stop --id ID
  workload restart --id ID
  workload force-stop --id ID
  workload update --id ID [--cpus N] [--memory-bytes N] [--autostart true|false]
  workload delete --id ID
  workload clone --id ID [--name NAME]
  node terminal [--id ID] [--cwd PATH]
  workload files ls --id ID [--path PATH]
  lab qemu-proto start [--pool-id ID] [--volume-id ID] [--size-bytes N] [--autostart]
  lab qemu-proto status
  lab qemu-proto stop
  lab qemu-proto kill
  cert status
  cert generate --cn NAME [--san HOST] --confirm enable-tls
  cert import --cert FILE --key FILE --confirm enable-tls
  cert acme --directory URL --email EMAIL --domain NAME --confirm enable-tls
  snapshot list --workload ID
  snapshot create --workload ID --name NAME
  snapshot rollback --id ID --confirm rollback
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
		if len(args) < 2 {
			return fmt.Errorf("usage: nodalctl node show|terminal")
		}
		switch args[1] {
		case "show":
			return cmdGet("/api/v1/nodes")
		case "terminal":
			return cmdNodeTerminal(args[2:])
		default:
			return fmt.Errorf("usage: nodalctl node show|terminal")
		}
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
	case "network":
		return cmdNetwork(args[1:])
	case "workload":
		return cmdWorkload(args[1:])
	case "lab":
		return cmdLab(args[1:])
	case "cert":
		return cmdCert(args[1:])
	case "snapshot":
		return cmdSnapshot(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func baseURL() string {
	if u := os.Getenv("NODAL_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	if _, err := os.Stat("/var/lib/ndl/certs/current.crt"); err == nil {
		return "https://127.0.0.1"
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

func patchJSON(path string, body any, saveSession bool) error {
	resp, err := do("PATCH", path, body, saveSession)
	if err != nil {
		return err
	}
	if len(resp) > 0 {
		fmt.Println(string(resp))
	}
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
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
	client := &http.Client{Jar: jar, Transport: nodalTransport()}
	if f := strings.TrimSpace(os.Getenv("NODAL_CONFIRM")); f != "" && req.Header.Get("X-Nodal-Confirm") == "" {
		req.Header.Set("X-Nodal-Confirm", f)
	}
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

func cmdNetwork(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nodalctl network list|create|apply ...")
	}
	switch args[0] {
	case "list":
		return cmdGet("/api/v1/networks")
	case "create":
		f := parseFlags(args[1:])
		body := map[string]any{
			"name": f["name"], "kind": f["kind"], "ipv4_cidr": f["cidr"],
			"uplink_ifname": f["uplink"], "confirm_ifname": f["confirm-ifname"],
			"dry_run": f["dry-run"] == "true",
		}
		headers := map[string]string{}
		if tok := f["confirm"]; tok != "" {
			headers["X-Nodal-Confirm"] = tok
		}
		return postJSONHeaders("/api/v1/networks", body, true, headers)
	case "apply":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl network apply --id ID [--dry-run]")
		}
		path := "/api/v1/networks/" + f["id"] + "/apply"
		if f["dry-run"] == "true" {
			path += "?dry_run=true"
		}
		body := map[string]any{"confirm_ifname": f["confirm-ifname"], "uplink_ifname": f["uplink"]}
		headers := map[string]string{}
		if tok := f["confirm"]; tok != "" {
			headers["X-Nodal-Confirm"] = tok
		}
		return postJSONHeaders(path, body, true, headers)
	default:
		return fmt.Errorf("unknown network command")
	}
}

func postJSONHeaders(path string, body any, saveSession bool, headers map[string]string) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest("POST", baseURL()+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if saveSession {
		if c, err := os.ReadFile(sessionFile()); err == nil {
			req.Header.Set("Cookie", strings.TrimSpace(string(c)))
		}
		if tok := os.Getenv("NODAL_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(out)))
	}
	if len(out) > 0 {
		fmt.Println(string(out))
	}
	return nil
}

func cmdWorkload(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nodalctl workload list|create|get|start|stop|restart|force-stop|update|delete|clone|files")
	}
	switch args[0] {
	case "list":
		return cmdGet("/api/v1/workloads")
	case "create":
		f := parseFlags(args[1:])
		kind := f["kind"]
		if kind == "" {
			kind = "system-container"
		}
		var cpus int
		var mem int64
		fmt.Sscan(f["cpus"], &cpus)
		fmt.Sscan(f["memory-bytes"], &mem)
		body := map[string]any{
			"name": f["name"], "kind": kind, "image_pin": f["image-pin"],
			"pool_id": f["pool-id"], "network_id": f["network-id"],
			"privileged": f["privileged"] == "true",
		}
		if kind == "vm" {
			body["firmware"] = firstNonEmpty(f["firmware"], "bios")
			body["autostart"] = f["autostart"] == "true"
			if f["cloud-image-id"] != "" {
				body["cloud_image_id"] = f["cloud-image-id"]
			}
			if f["iso-library-id"] != "" {
				body["iso_library_id"] = f["iso-library-id"]
			}
			nocloud := map[string]any{"enable": true}
			if f["nocloud-user"] != "" {
				nocloud["username"] = f["nocloud-user"]
			}
			if f["nocloud-host"] != "" {
				nocloud["hostname"] = f["nocloud-host"]
			} else if f["name"] != "" {
				nocloud["hostname"] = f["name"]
			}
			body["nocloud"] = nocloud
			delete(body, "image_pin")
			delete(body, "privileged")
		}
		if cpus > 0 {
			body["cpus"] = cpus
		}
		if mem > 0 {
			body["memory_bytes"] = mem
		}
		headers := map[string]string{}
		if key := f["idempotency-key"]; key != "" {
			headers["Idempotency-Key"] = key
		}
		return postJSONHeaders("/api/v1/workloads", body, true, headers)
	case "get":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl workload get --id ID")
		}
		return cmdGet("/api/v1/workloads/" + f["id"])
	case "start", "stop", "restart", "delete":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl workload %s --id ID", args[0])
		}
		headers := map[string]string{}
		if args[0] == "delete" {
			headers["X-Nodal-Confirm"] = "delete"
		}
		return postJSONHeaders("/api/v1/workloads/"+f["id"]+"/"+args[0], map[string]any{}, true, headers)
	case "force-stop":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl workload force-stop --id ID")
		}
		return postJSON("/api/v1/workloads/"+f["id"]+"/force-stop", map[string]any{}, true)
	case "update":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl workload update --id ID [--cpus N] [--memory-bytes N] [--autostart true|false]")
		}
		body := map[string]any{}
		var cpus int
		var mem int64
		fmt.Sscan(f["cpus"], &cpus)
		fmt.Sscan(f["memory-bytes"], &mem)
		if cpus > 0 {
			body["cpus"] = cpus
		}
		if mem > 0 {
			body["memory_bytes"] = mem
		}
		if f["autostart"] == "true" || f["autostart"] == "false" {
			body["autostart"] = f["autostart"] == "true"
		}
		return patchJSON("/api/v1/workloads/"+f["id"], body, true)
	case "clone":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl workload clone --id ID [--name NAME]")
		}
		return postJSON("/api/v1/workloads/"+f["id"]+"/clone", map[string]any{"name": f["name"]}, true)
	case "files":
		return cmdWorkloadFiles(args[1:])
	default:
		return fmt.Errorf("unknown workload command")
	}
}

func cmdNodeTerminal(args []string) error {
	f := parseFlags(args)
	id := f["id"]
	if id == "" {
		raw, err := do("GET", "/api/v1/nodes", nil, true)
		if err != nil {
			return err
		}
		var listed struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &listed); err != nil || len(listed.Items) == 0 {
			return fmt.Errorf("no local node is enrolled")
		}
		id = listed.Items[0].ID
	}
	cwd := f["cwd"]
	if cwd == "" {
		cwd = "/"
	}
	return postJSON("/api/v1/nodes/"+id+"/terminal/sessions", map[string]any{"cwd": cwd}, true)
}

func cmdWorkloadFiles(args []string) error {
	if len(args) < 1 || args[0] != "ls" {
		return fmt.Errorf("usage: nodalctl workload files ls --id ID [--path PATH]")
	}
	f := parseFlags(args[1:])
	if f["id"] == "" {
		return fmt.Errorf("usage: nodalctl workload files ls --id ID [--path PATH]")
	}
	p := f["path"]
	if p == "" {
		p = "/"
	}
	return cmdGet("/api/v1/workloads/" + f["id"] + "/files?path=" + strings.ReplaceAll(p, " ", "%20"))
}

func cmdLab(args []string) error {
	if len(args) < 2 || args[0] != "qemu-proto" {
		return fmt.Errorf("usage: nodalctl lab qemu-proto start|status|stop|kill")
	}
	switch args[1] {
	case "start":
		f := parseFlags(args[2:])
		body := map[string]any{}
		if f["pool-id"] != "" {
			body["pool_id"] = f["pool-id"]
		}
		if f["volume-id"] != "" {
			body["volume_id"] = f["volume-id"]
		}
		var size int64
		fmt.Sscan(f["size-bytes"], &size)
		if size > 0 {
			body["size_bytes"] = size
		}
		if f["autostart"] == "true" {
			body["autostart"] = true
		}
		return postJSON("/api/v1/lab/qemu-proto", body, true)
	case "status":
		return cmdGet("/api/v1/lab/qemu-proto")
	case "stop":
		return postJSON("/api/v1/lab/qemu-proto/stop", map[string]any{}, true)
	case "kill":
		return postJSON("/api/v1/lab/qemu-proto/kill", map[string]any{}, true)
	default:
		return fmt.Errorf("usage: nodalctl lab qemu-proto start|status|stop|kill")
	}
}

func nodalTransport() http.RoundTripper {
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	clone := tr.Clone()
	if os.Getenv("NODAL_TLS_INSECURE") == "1" || strings.HasPrefix(baseURL(), "https://127.0.0.1") {
		clone.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return clone
}

func cmdCert(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodalctl cert status|generate|import|acme")
	}
	switch args[0] {
	case "status":
		return cmdGet("/api/v1/certs")
	case "generate":
		f := parseFlags(args[1:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		sans := []string{}
		if f["san"] != "" {
			sans = []string{f["san"]}
		}
		return postJSON("/api/v1/certs/generate", map[string]any{"common_name": f["cn"], "sans": sans}, true)
	case "import":
		f := parseFlags(args[1:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		certPEM, err := os.ReadFile(f["cert"])
		if err != nil {
			return err
		}
		keyPEM, err := os.ReadFile(f["key"])
		if err != nil {
			return err
		}
		return postJSON("/api/v1/certs/import", map[string]any{"cert_pem": string(certPEM), "key_pem": string(keyPEM)}, true)
	case "acme":
		f := parseFlags(args[1:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		return postJSON("/api/v1/certs/acme", map[string]any{
			"directory": f["directory"], "email": f["email"], "domain": f["domain"],
		}, true)
	default:
		return fmt.Errorf("usage: nodalctl cert status|generate|import|acme")
	}
}

func cmdSnapshot(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodalctl snapshot create|rollback|list")
	}
	switch args[0] {
	case "list":
		f := parseFlags(args[1:])
		if f["workload"] == "" {
			return fmt.Errorf("usage: nodalctl snapshot list --workload ID")
		}
		return cmdGet("/api/v1/workloads/" + f["workload"] + "/snapshots")
	case "create":
		f := parseFlags(args[1:])
		if f["workload"] == "" || f["name"] == "" {
			return fmt.Errorf("usage: nodalctl snapshot create --workload ID --name NAME")
		}
		return postJSON("/api/v1/workloads/"+f["workload"]+"/snapshots", map[string]any{"name": f["name"]}, true)
	case "rollback":
		f := parseFlags(args[1:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl snapshot rollback --id ID --confirm rollback")
		}
		return postJSON("/api/v1/snapshots/"+f["id"]+"/rollback", map[string]any{}, true)
	default:
		return fmt.Errorf("usage: nodalctl snapshot create|rollback|list")
	}
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
