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

	"github.com/no-dal/ndl-ce/internal/identity"
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
  node maintain --id ID [--reason TEXT]
  node maintain exit --id ID
  cluster show
  cluster ha
  cluster ha replica --endpoint HOST [--dsn DSN]
  cluster fence --confirm fence
	cluster promote --confirm promote
  cluster update [--confirm cluster-update]
  feature list
  feature enable NAME [--confirm enable-k8s]
  feature disable NAME [--confirm disable-feature]
  kubernetes show
  kubernetes start --confirm start-kubelet
  kubernetes stop
  policy list
  policy create --name NAME [--threshold N]
  policy apply --id ID [--confirm apply-policy]
  ai ask --prompt TEXT [--profile-id ID]
  ai provider list
  ai provider add --name NAME [--kind KIND] [--endpoint URL] [--model MODEL]
  ai profile list
  ai profile add --name NAME [--provider-id ID]
  app list
  app import FILE
  app install --id ID [--name NAME] [--pool-id ID] [--network-id ID]
  app verify --id ID
  app policy [--set community-allowed|verified-only]
  cluster join-token create
  cluster join --token TOKEN [--url URL] [--hostname HOST]
  cluster node revoke --id ID
  cluster wg show
  cluster wg peer add --name NAME [--endpoint HOST:PORT]
  task list
  event list
  storage pool list
  storage pool create --name NAME --path PATH
  storage volume list
  storage volume create --pool-id ID --class CLASS --size-bytes N
  storage image list
  storage image upload --pool-id ID --kind KIND --file PATH
  storage zfs import --guid GUID [--name NAME]
  storage zfs create --name NAME --disk PATH
  storage zfs runtime
  storage lvm create --name NAME --disk PATH
  storage lvm runtime
  storage nfs add --name NAME --locator SERVER:/EXPORT
  storage smb add --name NAME --locator //SERVER/SHARE [--username USER]
  storage iscsi add --name NAME --iqn IQN --portal HOST:3260
  storage distributed attach --name NAME --locator MON[,MON]/POOL [--user USER] [--key KEY]
  storage distributed osd --disk PATH --confirm start-ceph-osd
  storage distributed runtime
  network list
  network create --name NAME --kind KIND [--cidr CIDR] [--uplink IF] [--dry-run] [--confirm-ifname IF]
  network apply --id ID [--dry-run] [--confirm-ifname IF]
  network vlan add --vid 20 [--network-id ID] [--access IF]
  network bond add --name NAME [--mode active-backup] --members IF,IF
  network policy create --name NAME --src-workload ID --dst-workload ID [--action deny]
  network policy apply --id ID
  guest status --id ID
  workload list
  workload create --kind system-container --name NAME --image-pin PIN --pool-id ID --network-id ID [--cpus N] [--memory-bytes N] [--privileged]
  workload create --kind vm --name NAME --network-id ID [--pool-id ID] [--cpus N] [--memory-bytes N] [--firmware bios|uefi] [--cloud-image-id ID] [--iso-library-id ID] [--autostart] [--nocloud-user USER] [--nocloud-host HOST]
  workload create --kind oci --name NAME --image-pin IMAGE [--registry-id ID] [--network-id ID] [--volume-id ID] [--cpus N] [--memory-bytes N] [--privileged]
  registry list
  registry add --name NAME --url URL [--username USER] [--password PASS] [--insecure]
  stack list
  stack import FILE [--name NAME] [--pool-id ID] [--network-id ID] [--registry-id ID] [--apply]
  stack get --id ID
  stack apply --id ID
  workload get --id ID
  workload start --id ID
  workload stop --id ID
  workload restart --id ID
  workload force-stop --id ID
  workload update --id ID [--cpus N] [--memory-bytes N] [--autostart true|false]
  workload delete --id ID
  workload clone --id ID [--name NAME]
  workload migrate --id ID --dest-node-id ID [--mode live|offline]
  workload import --library-id ID --network-id ID [--name NAME] [--pool-id ID] [--firmware bios|uefi]
  workload export --id ID [--display-name NAME]
  node terminal [--id ID] [--cwd PATH]
  workload files ls --id ID [--path PATH]
  workload terminal --id ID [--cwd PATH]
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
  backup target list
  backup target create --kind r2 --name NAME --endpoint URL --bucket BUCKET --username KEY --password SECRET [--prefix PREFIX] [--region REGION] [--no-check-bucket]
  backup run --workload ID --target ID [--policy ID]
  backup restore --artifact ID --mode new|replace [--node ID] [--confirm restore]
  backup dr-export
  backup verify --artifact ID [--mode open|throwaway]
  backup restore-file --artifact ID --path PATH
  update check
  update apply --confirm apply-update
  update preflight
  update checkpoint
  update rollback --confirm rollback-update
  user mfa
  user mfa enroll
  user mfa confirm --code CODE
  user mfa verify --challenge-id ID --token TOKEN --code CODE
  group add --name NAME
  group list
  group member add --id ID --user-id USER
  group role bind --id ID --role operator|viewer
  gpu list
  gpu assign --gpu-id BDF --workload-id ID --mode render|compute|encode|vfio [--exclusive]
  logs [--unit UNIT] [--lines N]
  metrics query [--from RFC3339] [--to RFC3339]
  alert list
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
			return fmt.Errorf("usage: nodalctl node show|terminal|maintain")
		}
		switch args[1] {
		case "show":
			return cmdGet("/api/v1/nodes")
		case "terminal":
			return cmdNodeTerminal(args[2:])
		case "maintain":
			return cmdNodeMaintain(args[2:])
		default:
			return fmt.Errorf("usage: nodalctl node show|terminal|maintain")
		}
	case "cluster":
		return cmdCluster(args[1:])
	case "feature":
		return cmdFeature(args[1:])
	case "kubernetes":
		return cmdKubernetes(args[1:])
	case "policy":
		return cmdPolicy(args[1:])
	case "ai":
		return cmdAI(args[1:])
	case "app":
		return cmdApp(args[1:])
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
	case "guest":
		if len(args) < 2 || args[1] != "status" {
			return fmt.Errorf("usage: nodalctl guest status --id ID")
		}
		return cmdGuestStatus(args[2:])
	case "workload":
		return cmdWorkload(args[1:])
	case "registry":
		return cmdRegistry(args[1:])
	case "stack":
		return cmdStack(args[1:])
	case "lab":
		return cmdLab(args[1:])
	case "cert":
		return cmdCert(args[1:])
	case "snapshot":
		return cmdSnapshot(args[1:])
	case "backup":
		return cmdBackup(args[1:])
	case "update":
		return cmdUpdate(args[1:])
	case "user":
		return cmdUser(args[1:])
	case "group":
		return cmdGroup(args[1:])
	case "gpu":
		return cmdGPU(args[1:])
	case "logs":
		return cmdLogs(args[1:])
	case "metrics":
		if len(args) < 2 || args[1] != "query" {
			return fmt.Errorf("usage: nodalctl metrics query [--from RFC3339] [--to RFC3339]")
		}
		return cmdMetricsQuery(args[2:])
	case "alert":
		if len(args) < 2 || args[1] != "list" {
			return fmt.Errorf("usage: nodalctl alert list")
		}
		return cmdGet("/api/v1/alerts")
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

func cmdNodeMaintain(args []string) error {
	if len(args) > 0 && args[0] == "exit" {
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl node maintain exit --id ID")
		}
		return postJSON("/api/v1/nodes/"+f["id"]+"/maintain/exit", map[string]any{}, true)
	}
	f := parseFlags(args)
	if f["id"] == "" {
		return fmt.Errorf("usage: nodalctl node maintain --id ID [--reason TEXT]")
	}
	return postJSON("/api/v1/nodes/"+f["id"]+"/maintain", map[string]any{"reason": f["reason"]}, true)
}

func cmdGuestStatus(args []string) error {
	f := parseFlags(args)
	id := strings.TrimSpace(f["id"])
	if id == "" {
		return fmt.Errorf("usage: nodalctl guest status --id ID")
	}
	return cmdGet("/api/v1/workloads/" + id + "/guest")
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

func putJSON(path string, body any, saveSession bool) error {
	resp, err := do("PUT", path, body, saveSession)
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
		return fmt.Errorf("usage: nodalctl storage pool|volume|image|zfs|lvm|nfs|smb|iscsi|distributed ...")
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
	case "zfs import":
		f := parseFlags(args[2:])
		guid := firstNonEmpty(f["guid"], f["zpool-guid"])
		if guid == "" {
			return fmt.Errorf("usage: nodalctl storage zfs import --guid GUID [--name NAME]")
		}
		body := map[string]any{"guid": guid, "name": f["name"]}
		return postJSON("/api/v1/storage/zfs/import", body, true)
	case "zfs create":
		f := parseFlags(args[2:])
		if f["name"] == "" || f["disk"] == "" {
			return fmt.Errorf("usage: nodalctl storage zfs create --name NAME --disk PATH")
		}
		return postJSON("/api/v1/storage/zfs/create", map[string]any{"name": f["name"], "disks": []string{f["disk"]}}, true)
	case "zfs runtime":
		return cmdGet("/api/v1/storage/zfs")
	case "lvm create":
		f := parseFlags(args[2:])
		if f["name"] == "" || f["disk"] == "" {
			return fmt.Errorf("usage: nodalctl storage lvm create --name NAME --disk PATH")
		}
		return postJSON("/api/v1/storage/lvm/create", map[string]any{"name": f["name"], "disks": []string{f["disk"]}}, true)
	case "lvm runtime":
		return cmdGet("/api/v1/storage/lvm")
	case "nfs add":
		f := parseFlags(args[2:])
		if f["name"] == "" || f["locator"] == "" {
			return fmt.Errorf("usage: nodalctl storage nfs add --name NAME --locator SERVER:/EXPORT")
		}
		return postJSON("/api/v1/storage/nfs", map[string]any{"name": f["name"], "locator": f["locator"]}, true)
	case "smb add":
		f := parseFlags(args[2:])
		if f["name"] == "" || f["locator"] == "" {
			return fmt.Errorf("usage: nodalctl storage smb add --name NAME --locator //SERVER/SHARE [--username USER]")
		}
		return postJSON("/api/v1/storage/smb", map[string]any{"name": f["name"], "locator": f["locator"], "username": f["username"], "password": f["password"]}, true)
	case "iscsi add":
		f := parseFlags(args[2:])
		if f["name"] == "" || f["iqn"] == "" || f["portal"] == "" {
			return fmt.Errorf("usage: nodalctl storage iscsi add --name NAME --iqn IQN --portal HOST:3260")
		}
		return postJSON("/api/v1/storage/iscsi", map[string]any{"name": f["name"], "iqn": f["iqn"], "portal": f["portal"]}, true)
	case "distributed attach":
		f := parseFlags(args[2:])
		if f["name"] == "" || f["locator"] == "" {
			return fmt.Errorf("usage: nodalctl storage distributed attach --name NAME --locator MON[,MON]/POOL [--user USER]")
		}
		body := map[string]any{"name": f["name"], "locator": f["locator"], "user": f["user"], "cephx_key": firstNonEmpty(f["key"], f["cephx-key"])}
		return postJSON("/api/v1/storage/distributed", body, true)
	case "distributed runtime":
		return cmdGet("/api/v1/storage/distributed")
	case "distributed osd":
		f := parseFlags(args[2:])
		if f["disk"] == "" {
			return fmt.Errorf("usage: nodalctl storage distributed osd --disk PATH --confirm start-ceph-osd")
		}
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		return postJSON("/api/v1/storage/distributed/osds", map[string]any{"disk": f["disk"], "pool_id": f["pool-id"]}, true)
	case "distributed osd-start":
		f := parseFlags(args[2:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		return postJSON("/api/v1/storage/distributed/osds/start", map[string]any{}, true)
	case "distributed osd-stop":
		return postJSON("/api/v1/storage/distributed/osds/stop", map[string]any{}, true)
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
		return fmt.Errorf("usage: nodalctl network list|create|apply|vlan add|bond add|policy ...")
	}
	cmd := args[0]
	if len(args) > 1 && (args[1] == "add" || args[1] == "create" || args[1] == "apply") {
		cmd = args[0] + " " + args[1]
		args = args[1:]
	}
	switch cmd {
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
	case "vlan add":
		f := parseFlags(args[1:])
		if f["vid"] == "" {
			return fmt.Errorf("usage: nodalctl network vlan add --vid 20 [--network-id ID] [--access IF]")
		}
		vid := 0
		fmt.Sscanf(f["vid"], "%d", &vid)
		return postJSON("/api/v1/networks/vlans", map[string]any{
			"network_id": f["network-id"], "vlan_id": vid, "access_ifname": f["access"],
			"parent_ifname": f["parent"], "name": f["name"], "confirm_ifname": f["confirm-ifname"],
		}, true)
	case "bond add":
		f := parseFlags(args[1:])
		if f["name"] == "" || f["members"] == "" {
			return fmt.Errorf("usage: nodalctl network bond add --name NAME --members IF,IF")
		}
		return postJSON("/api/v1/networks/bonds", map[string]any{
			"name": f["name"], "mode": f["mode"], "members": strings.Split(f["members"], ","),
			"confirm_ifname": f["confirm-ifname"],
		}, true)
	case "policy create":
		f := parseFlags(args[1:])
		if f["name"] == "" || f["src-workload"] == "" || f["dst-workload"] == "" {
			return fmt.Errorf("usage: nodalctl network policy create --name NAME --src-workload ID --dst-workload ID")
		}
		return postJSON("/api/v1/networks/policies", map[string]any{
			"name": f["name"], "action": f["action"], "src_workload_id": f["src-workload"], "dst_workload_id": f["dst-workload"],
		}, true)
	case "policy apply":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl network policy apply --id ID")
		}
		return postJSON("/api/v1/networks/policies/"+f["id"]+"/apply", map[string]any{}, true)
	default:
		return fmt.Errorf("unknown network command")
	}
}

func cmdCluster(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nodalctl cluster show|ha|fence|promote|update|join-token create|join|node revoke|wg show|peer add")
	}
	switch args[0] {
	case "show":
		return cmdGet("/api/v1/cluster")
	case "ha":
		if len(args) == 1 {
			return cmdGet("/api/v1/cluster/ha")
		}
		if args[1] != "replica" {
			return fmt.Errorf("usage: nodalctl cluster ha [replica --endpoint HOST [--dsn DSN]]")
		}
		f := parseFlags(args[2:])
		if f["endpoint"] == "" && f["dsn"] == "" {
			return fmt.Errorf("usage: nodalctl cluster ha replica --endpoint HOST [--dsn DSN]")
		}
		body := map[string]any{}
		if f["endpoint"] != "" {
			body["endpoint"] = f["endpoint"]
		}
		if f["dsn"] != "" {
			body["dsn"] = f["dsn"]
		}
		return postJSON("/api/v1/cluster/ha/replica", body, true)
	case "fence":
		f := parseFlags(args[1:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		if os.Getenv("NODAL_CONFIRM") == "" {
			return fmt.Errorf("usage: nodalctl cluster fence --confirm fence")
		}
		return postJSON("/api/v1/cluster/ha/fence", map[string]any{}, true)
	case "promote":
		f := parseFlags(args[1:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		if os.Getenv("NODAL_CONFIRM") == "" {
			return fmt.Errorf("usage: nodalctl cluster promote --confirm promote")
		}
		return postJSON("/api/v1/cluster/ha/promote", map[string]any{}, true)
	case "update":
		f := parseFlags(args[1:])
		if f["confirm"] == "" {
			return cmdGet("/api/v1/cluster/update")
		}
		_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		return postJSON("/api/v1/cluster/update", map[string]any{}, true)
	case "join-token":
		if len(args) < 2 || args[1] != "create" {
			return fmt.Errorf("usage: nodalctl cluster join-token create")
		}
		return postJSON("/api/v1/cluster/join-tokens", map[string]any{}, true)
	case "join":
		return cmdClusterJoin(args[1:])
	case "node":
		if len(args) < 2 || args[1] != "revoke" {
			return fmt.Errorf("usage: nodalctl cluster node revoke --id ID")
		}
		f := parseFlags(args[2:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl cluster node revoke --id ID")
		}
		return postJSON("/api/v1/cluster/nodes/"+f["id"]+"/revoke", map[string]any{}, true)
	case "wg":
		if len(args) < 2 {
			return fmt.Errorf("usage: nodalctl cluster wg show|peer add")
		}
		switch args[1] {
		case "show":
			return cmdGet("/api/v1/cluster/wg")
		case "peer":
			if len(args) < 3 || args[2] != "add" {
				return fmt.Errorf("usage: nodalctl cluster wg peer add --name NAME [--endpoint HOST:PORT]")
			}
			f := parseFlags(args[3:])
			if f["name"] == "" {
				return fmt.Errorf("usage: nodalctl cluster wg peer add --name NAME [--endpoint HOST:PORT]")
			}
			body := map[string]any{"name": f["name"], "endpoint": f["endpoint"]}
			if f["local-address"] != "" {
				body["local_address"] = f["local-address"]
			}
			if f["worker-address"] != "" {
				body["worker_address"] = f["worker-address"]
			}
			return postJSON("/api/v1/cluster/wg/peers", body, true)
		default:
			return fmt.Errorf("usage: nodalctl cluster wg show|peer add")
		}
	default:
		return fmt.Errorf("usage: nodalctl cluster show|ha|fence|promote|update|join-token create|join|node revoke|wg show|peer add")
	}
}

func cmdFeature(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nodalctl feature list|enable NAME|disable NAME")
	}
	switch args[0] {
	case "list":
		return cmdGet("/api/v1/features")
	case "enable":
		if len(args) < 2 {
			return fmt.Errorf("usage: nodalctl feature enable oci|gpu|k8s|kubernetes|distributed_storage|ai [--confirm enable-k8s]")
		}
		f := parseFlags(args[2:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		return postJSON("/api/v1/features/"+args[1]+"/enable", map[string]any{}, true)
	case "disable":
		if len(args) < 2 {
			return fmt.Errorf("usage: nodalctl feature disable NAME [--confirm disable-feature]")
		}
		f := parseFlags(args[2:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		return postJSON("/api/v1/features/"+args[1]+"/disable", map[string]any{}, true)
	default:
		return fmt.Errorf("usage: nodalctl feature list|enable NAME|disable NAME")
	}
}

func cmdKubernetes(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nodalctl kubernetes show|start|stop")
	}
	switch args[0] {
	case "show":
		return cmdGet("/api/v1/kubernetes")
	case "start":
		f := parseFlags(args[1:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		return postJSON("/api/v1/kubernetes/start", map[string]any{}, true)
	case "stop":
		return postJSON("/api/v1/kubernetes/stop", map[string]any{}, true)
	default:
		return fmt.Errorf("usage: nodalctl kubernetes show|start|stop")
	}
}

func cmdPolicy(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nodalctl policy list|create|apply")
	}
	switch args[0] {
	case "list":
		return cmdGet("/api/v1/policies")
	case "create":
		f := parseFlags(args[1:])
		if f["name"] == "" {
			return fmt.Errorf("usage: nodalctl policy create --name NAME [--threshold N]")
		}
		var threshold int
		if f["threshold"] != "" {
			fmt.Sscan(f["threshold"], &threshold)
		}
		body := map[string]any{
			"name": f["name"], "kind": "storage_pressure", "action": "enqueue_migrate_low_priority",
			"threshold_percent": threshold, "require_approval": f["require-approval"] == "true",
		}
		return postJSON("/api/v1/policies", body, true)
	case "apply":
		f := parseFlags(args[1:])
		id := f["id"]
		if id == "" && len(args) > 1 && !strings.HasPrefix(args[1], "--") {
			id = args[1]
		}
		if id == "" {
			return fmt.Errorf("usage: nodalctl policy apply --id ID [--confirm apply-policy]")
		}
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		return postJSON("/api/v1/policies/"+id+"/apply", map[string]any{}, true)
	default:
		return fmt.Errorf("usage: nodalctl policy list|create|apply")
	}
}

func cmdAI(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nodalctl ai ask|provider|profile")
	}
	switch args[0] {
	case "ask":
		f := parseFlags(args[1:])
		prompt := f["prompt"]
		if prompt == "" {
			return fmt.Errorf("usage: nodalctl ai ask --prompt TEXT [--profile-id ID]")
		}
		body := map[string]any{"prompt": prompt, "profile_id": f["profile-id"]}
		return postJSON("/api/v1/ai/ask", body, true)
	case "provider":
		if len(args) < 2 {
			return fmt.Errorf("usage: nodalctl ai provider list|add")
		}
		switch args[1] {
		case "list":
			return cmdGet("/api/v1/ai/providers")
		case "add":
			f := parseFlags(args[2:])
			if f["name"] == "" {
				return fmt.Errorf("usage: nodalctl ai provider add --name NAME [--kind KIND] [--endpoint URL] [--model MODEL]")
			}
			kind := f["kind"]
			if kind == "" {
				kind = "local"
			}
			body := map[string]any{"name": f["name"], "kind": kind, "endpoint": f["endpoint"], "model": f["model"], "api_key": f["key"]}
			return postJSON("/api/v1/ai/providers", body, true)
		default:
			return fmt.Errorf("usage: nodalctl ai provider list|add")
		}
	case "profile":
		if len(args) < 2 {
			return fmt.Errorf("usage: nodalctl ai profile list|add")
		}
		switch args[1] {
		case "list":
			return cmdGet("/api/v1/ai/profiles")
		case "add":
			f := parseFlags(args[2:])
			if f["name"] == "" {
				return fmt.Errorf("usage: nodalctl ai profile add --name NAME [--provider-id ID]")
			}
			body := map[string]any{"name": f["name"], "provider_id": f["provider-id"], "mode": "ask"}
			return postJSON("/api/v1/ai/profiles", body, true)
		default:
			return fmt.Errorf("usage: nodalctl ai profile list|add")
		}
	default:
		return fmt.Errorf("usage: nodalctl ai ask|provider|profile")
	}
}

func cmdApp(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nodalctl app list|import FILE|install --id ID|verify --id ID|policy")
	}
	switch args[0] {
	case "list":
		return cmdGet("/api/v1/store/apps")
	case "import":
		if len(args) < 2 {
			return fmt.Errorf("usage: nodalctl app import FILE")
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		return postJSON("/api/v1/store/apps/import", map[string]any{"manifest": string(raw)}, true)
	case "install":
		f := parseFlags(args[1:])
		id := f["id"]
		if id == "" {
			return fmt.Errorf("usage: nodalctl app install --id ID [--name NAME] [--pool-id ID] [--network-id ID]")
		}
		body := map[string]any{"name": f["name"], "pool_id": f["pool-id"], "network_id": f["network-id"], "node_id": f["node-id"]}
		return postJSON("/api/v1/store/apps/"+id+"/install", body, true)
	case "verify":
		f := parseFlags(args[1:])
		id := f["id"]
		if id == "" {
			return fmt.Errorf("usage: nodalctl app verify --id ID")
		}
		return postJSON("/api/v1/store/apps/"+id+"/verify", map[string]any{}, true)
	case "policy":
		f := parseFlags(args[1:])
		if f["set"] != "" {
			return putJSON("/api/v1/store/policy", map[string]any{"install_policy": f["set"]}, true)
		}
		return cmdGet("/api/v1/store/policy")
	default:
		return fmt.Errorf("usage: nodalctl app list|import FILE|install --id ID|verify --id ID|policy")
	}
}

func cmdClusterJoin(args []string) error {
	f := parseFlags(args)
	if f["token"] == "" {
		return fmt.Errorf("usage: nodalctl cluster join --token TOKEN [--url URL] [--hostname HOST]")
	}
	hostname := f["hostname"]
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	url := strings.TrimRight(f["url"], "/")
	if url == "" {
		url = strings.TrimRight(baseURL(), "/")
	}
	body, err := json.Marshal(map[string]any{"token": f["token"], "hostname": hostname})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", url+"/api/v1/cluster/join", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: nodalTransport()}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(out)))
	}
	var joined struct {
		ID        string `json:"id"`
		ClusterID string `json:"cluster_id"`
		CACert    string `json:"ca_cert"`
		NodeCert  string `json:"node_cert"`
		NodeKey   string `json:"node_key"`
	}
	if err := json.Unmarshal(out, &joined); err != nil {
		return err
	}
	dir := os.Getenv("NODAL_DATA_DIR")
	if dir == "" {
		dir = "/var/lib/ndl"
	}
	ident := identity.Files{Dir: dir}
	if err := ident.SaveJoinMaterial(joined.ClusterID, joined.ID, []byte(joined.CACert), []byte(joined.NodeCert), []byte(joined.NodeKey)); err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
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
		return fmt.Errorf("usage: nodalctl workload list|create|get|start|stop|restart|force-stop|update|delete|clone|migrate|import|export|files|terminal")
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
		if kind == "oci" {
			if f["registry-id"] != "" {
				body["registry_id"] = f["registry-id"]
			}
			if f["volume-id"] != "" {
				body["volume_ids"] = []string{f["volume-id"]}
			}
			delete(body, "pool_id")
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
	case "migrate":
		f := parseFlags(args[1:])
		if f["id"] == "" || f["dest-node-id"] == "" {
			return fmt.Errorf("usage: nodalctl workload migrate --id ID --dest-node-id ID [--mode live|offline]")
		}
		body := map[string]any{"dest_node_id": f["dest-node-id"]}
		if f["mode"] != "" {
			body["mode"] = f["mode"]
		}
		return postJSON("/api/v1/workloads/"+f["id"]+"/migrate", body, true)
	case "import":
		f := parseFlags(args[1:])
		if f["library-id"] == "" || f["network-id"] == "" {
			return fmt.Errorf("usage: nodalctl workload import --library-id ID --network-id ID [--name NAME] [--pool-id ID] [--firmware bios|uefi]")
		}
		body := map[string]any{
			"library_id": f["library-id"], "network_id": f["network-id"], "name": f["name"],
			"pool_id": f["pool-id"], "firmware": f["firmware"],
		}
		return postJSON("/api/v1/workloads/import", body, true)
	case "export":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl workload export --id ID [--display-name NAME]")
		}
		return postJSON("/api/v1/workloads/"+f["id"]+"/export", map[string]any{"display_name": f["display-name"]}, true)
	case "files":
		return cmdWorkloadFiles(args[1:])
	case "terminal":
		return cmdWorkloadTerminal(args[1:])
	default:
		return fmt.Errorf("unknown workload command")
	}
}

func cmdRegistry(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nodalctl registry list|add")
	}
	switch args[0] {
	case "list":
		return cmdGet("/api/v1/registries")
	case "add":
		f := parseFlags(args[1:])
		if f["name"] == "" || f["url"] == "" {
			return fmt.Errorf("usage: nodalctl registry add --name NAME --url URL [--username USER] [--password PASS] [--insecure]")
		}
		body := map[string]any{
			"name": f["name"], "url": f["url"],
			"username": f["username"], "password": f["password"],
			"insecure": f["insecure"] == "true",
		}
		return postJSON("/api/v1/registries", body, true)
	default:
		return fmt.Errorf("usage: nodalctl registry list|add")
	}
}

func cmdStack(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: nodalctl stack list|import|get|apply|patch-member")
	}
	switch args[0] {
	case "list":
		return cmdGet("/api/v1/stacks")
	case "import":
		f := parseFlags(args[1:])
		path := f["file"]
		if path == "" && len(args) > 1 && !strings.HasPrefix(args[1], "--") {
			path = args[1]
			f = parseFlags(args[2:])
		}
		if path == "" {
			return fmt.Errorf("usage: nodalctl stack import FILE [--name NAME] [--pool-id ID] [--network-id ID] [--registry-id ID] [--apply]")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := f["name"]
		if name == "" {
			base := filepath.Base(path)
			name = strings.TrimSuffix(base, filepath.Ext(base))
			if name == "" {
				name = "stack"
			}
		}
		body := map[string]any{
			"name": name, "compose": string(raw),
			"pool_id": f["pool-id"], "network_id": f["network-id"], "registry_id": f["registry-id"],
			"apply": f["apply"] == "true",
		}
		return postJSON("/api/v1/stacks/import", body, true)
	case "get":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl stack get --id ID")
		}
		return cmdGet("/api/v1/stacks/" + f["id"])
	case "apply":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl stack apply --id ID")
		}
		return postJSON("/api/v1/stacks/"+f["id"]+"/apply", map[string]any{}, true)
	case "patch-member":
		f := parseFlags(args[1:])
		if f["id"] == "" || f["member"] == "" {
			return fmt.Errorf("usage: nodalctl stack patch-member --id STACK --member MEMBER [--image-pin REF] [--name NAME]")
		}
		body := map[string]any{}
		if f["image-pin"] != "" {
			body["image_pin"] = f["image-pin"]
		}
		if f["name"] != "" {
			body["name"] = f["name"]
		}
		if len(body) == 0 {
			return fmt.Errorf("usage: nodalctl stack patch-member --id STACK --member MEMBER [--image-pin REF] [--name NAME]")
		}
		return patchJSON("/api/v1/stacks/"+f["id"]+"/members/"+f["member"], body, true)
	default:
		return fmt.Errorf("usage: nodalctl stack list|import|get|apply|patch-member")
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

func cmdWorkloadTerminal(args []string) error {
	f := parseFlags(args)
	if f["id"] == "" {
		return fmt.Errorf("usage: nodalctl workload terminal --id ID [--cwd PATH]")
	}
	cwd := f["cwd"]
	if cwd == "" {
		cwd = "/"
	}
	return postJSON("/api/v1/workloads/"+f["id"]+"/terminal/sessions", map[string]any{"cwd": cwd}, true)
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

func cmdBackup(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodalctl backup target|run|restore|dr-export|verify|restore-file")
	}
	switch args[0] {
	case "target":
		return cmdBackupTarget(args[1:])
	case "run":
		f := parseFlags(args[1:])
		if f["workload"] == "" || f["target"] == "" {
			return fmt.Errorf("usage: nodalctl backup run --workload ID --target ID [--policy ID]")
		}
		body := map[string]any{"workload_id": f["workload"], "target_id": f["target"]}
		if f["policy"] != "" {
			body["policy_id"] = f["policy"]
		}
		return postJSON("/api/v1/backups/run", body, true)
	case "restore":
		f := parseFlags(args[1:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		if f["artifact"] == "" || (f["mode"] != "new" && f["mode"] != "replace") {
			return fmt.Errorf("usage: nodalctl backup restore --artifact ID --mode new|replace [--node ID] [--confirm restore]")
		}
		body := map[string]any{"mode": f["mode"]}
		if f["node"] != "" {
			body["target_node_id"] = f["node"]
		}
		return postJSON("/api/v1/backups/artifacts/"+f["artifact"]+"/restore", body, true)
	case "dr-export":
		return cmdGet("/api/v1/backups/dr-export")
	case "verify":
		f := parseFlags(args[1:])
		if f["artifact"] == "" {
			return fmt.Errorf("usage: nodalctl backup verify --artifact ID [--mode open|throwaway]")
		}
		mode := f["mode"]
		if mode == "" {
			mode = "open"
		}
		if mode != "open" && mode != "throwaway" {
			return fmt.Errorf("usage: nodalctl backup verify --artifact ID [--mode open|throwaway]")
		}
		return postJSON("/api/v1/backups/artifacts/"+f["artifact"]+"/verify", map[string]any{"mode": mode}, true)
	case "restore-file":
		f := parseFlags(args[1:])
		if f["artifact"] == "" || f["path"] == "" {
			return fmt.Errorf("usage: nodalctl backup restore-file --artifact ID --path PATH")
		}
		return postJSON("/api/v1/backups/artifacts/"+f["artifact"]+"/restore-file", map[string]any{"path": f["path"]}, true)
	default:
		return fmt.Errorf("usage: nodalctl backup target|run|restore|dr-export|verify|restore-file")
	}
}

func cmdBackupTarget(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodalctl backup target create|list")
	}
	switch args[0] {
	case "list":
		return cmdGet("/api/v1/backups/targets")
	case "create":
		f := parseFlags(args[1:])
		kind := strings.ToLower(strings.TrimSpace(f["kind"]))
		if f["name"] == "" || kind == "" {
			return fmt.Errorf("usage: nodalctl backup target create --kind r2 --name NAME --endpoint URL --bucket BUCKET --username KEY --password SECRET [--prefix PREFIX] [--region REGION] [--no-check-bucket]")
		}
		body := map[string]any{"name": f["name"], "kind": kind}
		if f["locator"] != "" {
			body["locator"] = f["locator"]
		}
		if f["endpoint"] != "" {
			body["endpoint"] = f["endpoint"]
		}
		if f["bucket"] != "" {
			body["bucket"] = f["bucket"]
		}
		if f["prefix"] != "" {
			body["prefix"] = f["prefix"]
		}
		if f["region"] != "" {
			body["region"] = f["region"]
		}
		if f["username"] != "" {
			body["username"] = f["username"]
		}
		if f["password"] != "" {
			body["password"] = f["password"]
		}
		if f["encryption-key"] != "" {
			body["encryption_key"] = f["encryption-key"]
		}
		if f["no-check-bucket"] == "true" || f["no-check-bucket"] == "1" {
			body["no_check_bucket"] = true
		}
		switch kind {
		case "s3", "r2", "aws", "b2", "minio":
			if f["endpoint"] == "" || f["bucket"] == "" || f["username"] == "" || f["password"] == "" {
				return fmt.Errorf("usage: nodalctl backup target create --kind r2 --name NAME --endpoint URL --bucket BUCKET --username KEY --password SECRET [--prefix PREFIX] [--region REGION] [--no-check-bucket]")
			}
		default:
			if f["locator"] == "" {
				return fmt.Errorf("usage: nodalctl backup target create --kind local --name NAME --locator PATH")
			}
		}
		return postJSON("/api/v1/backups/targets", body, true)
	default:
		return fmt.Errorf("usage: nodalctl backup target create|list")
	}
}

func cmdUpdate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodalctl update check|apply|preflight|checkpoint|rollback")
	}
	switch args[0] {
	case "check":
		return postJSON("/api/v1/updates/check", map[string]any{}, true)
	case "preflight":
		return postJSON("/api/v1/updates/preflight", map[string]any{}, true)
	case "checkpoint":
		return postJSON("/api/v1/updates/checkpoint", map[string]any{}, true)
	case "apply":
		f := parseFlags(args[1:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		if os.Getenv("NODAL_CONFIRM") == "" {
			return fmt.Errorf("usage: nodalctl update apply --confirm apply-update")
		}
		return postJSON("/api/v1/updates/apply", map[string]any{}, true)
	case "rollback":
		f := parseFlags(args[1:])
		if f["confirm"] != "" {
			_ = os.Setenv("NODAL_CONFIRM", f["confirm"])
		}
		if os.Getenv("NODAL_CONFIRM") == "" {
			return fmt.Errorf("usage: nodalctl update rollback --confirm rollback-update")
		}
		return postJSON("/api/v1/updates/rollback", map[string]any{}, true)
	default:
		return fmt.Errorf("usage: nodalctl update check|apply|preflight|checkpoint|rollback")
	}
}

func cmdUser(args []string) error {
	if len(args) == 0 || args[0] != "mfa" {
		return fmt.Errorf("usage: nodalctl user mfa [enroll|confirm|verify]")
	}
	rest := args[1:]
	if len(rest) == 0 {
		return cmdGet("/api/v1/mfa")
	}
	switch rest[0] {
	case "enroll":
		return postJSON("/api/v1/mfa/enroll", map[string]any{}, true)
	case "confirm":
		f := parseFlags(rest[1:])
		if f["code"] == "" {
			return fmt.Errorf("usage: nodalctl user mfa confirm --code CODE")
		}
		return postJSON("/api/v1/mfa/confirm", map[string]any{"code": f["code"]}, true)
	case "verify":
		f := parseFlags(rest[1:])
		challenge := firstNonEmpty(f["challenge-id"], f["mfa-challenge-id"])
		token := firstNonEmpty(f["token"], f["mfa-token"])
		if challenge == "" || token == "" || f["code"] == "" {
			return fmt.Errorf("usage: nodalctl user mfa verify --challenge-id ID --token TOKEN --code CODE")
		}
		return postJSON("/api/v1/auth/mfa/verify", map[string]any{
			"mfa_challenge_id": challenge,
			"mfa_token":        token,
			"code":             f["code"],
		}, true)
	default:
		return fmt.Errorf("usage: nodalctl user mfa [enroll|confirm|verify]")
	}
}

func cmdGroup(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodalctl group add --name NAME")
	}
	switch args[0] {
	case "add":
		f := parseFlags(args[1:])
		if f["name"] == "" {
			return fmt.Errorf("usage: nodalctl group add --name NAME")
		}
		return postJSON("/api/v1/groups", map[string]any{"name": f["name"]}, true)
	case "list":
		return cmdGet("/api/v1/groups")
	case "member":
		if len(args) < 2 || args[1] != "add" {
			return fmt.Errorf("usage: nodalctl group member add --id ID --user-id USER")
		}
		f := parseFlags(args[2:])
		userID := firstNonEmpty(f["user-id"], f["user_id"])
		if f["id"] == "" || userID == "" {
			return fmt.Errorf("usage: nodalctl group member add --id ID --user-id USER")
		}
		return postJSON("/api/v1/groups/"+f["id"]+"/members", map[string]any{"user_id": userID}, true)
	case "role":
		if len(args) < 2 || args[1] != "bind" {
			return fmt.Errorf("usage: nodalctl group role bind --id ID --role operator|viewer")
		}
		f := parseFlags(args[2:])
		if f["id"] == "" || f["role"] == "" {
			return fmt.Errorf("usage: nodalctl group role bind --id ID --role operator|viewer")
		}
		return postJSON("/api/v1/groups/"+f["id"]+"/roles", map[string]any{"role": f["role"]}, true)
	default:
		return fmt.Errorf("usage: nodalctl group add --name NAME")
	}
}

func cmdGPU(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodalctl gpu list|assign|unassign")
	}
	switch args[0] {
	case "list":
		return cmdGet("/api/v1/gpus")
	case "assign":
		f := parseFlags(args[1:])
		gpuID := firstNonEmpty(f["gpu-id"], f["gpu_id"])
		wl := firstNonEmpty(f["workload-id"], f["workload_id"])
		if gpuID == "" || wl == "" || f["mode"] == "" {
			return fmt.Errorf("usage: nodalctl gpu assign --gpu-id BDF --workload-id ID --mode render|compute|encode|vfio")
		}
		body := map[string]any{"gpu_id": gpuID, "workload_id": wl, "mode": f["mode"], "exclusive": f["exclusive"] != "false"}
		return postJSON("/api/v1/gpus/assign", body, true)
	case "unassign":
		f := parseFlags(args[1:])
		if f["id"] == "" {
			return fmt.Errorf("usage: nodalctl gpu unassign --id ID")
		}
		return postJSON("/api/v1/gpus/unassign", map[string]any{"id": f["id"]}, true)
	default:
		return fmt.Errorf("usage: nodalctl gpu list|assign|unassign")
	}
}

func cmdLogs(args []string) error {
	f := parseFlags(args)
	id, err := defaultNodeID(f["id"])
	if err != nil {
		return err
	}
	unit := f["unit"]
	if unit == "" {
		unit = "ndl-agent.service"
	}
	path := "/api/v1/nodes/" + id + "/logs?unit=" + unit
	if f["lines"] != "" {
		path += "&lines=" + f["lines"]
	}
	return cmdGet(path)
}

func cmdMetricsQuery(args []string) error {
	f := parseFlags(args)
	id, err := defaultNodeID(f["id"])
	if err != nil {
		return err
	}
	path := "/api/v1/nodes/" + id + "/metrics"
	var q []string
	if f["from"] != "" {
		q = append(q, "from="+f["from"])
	}
	if f["to"] != "" {
		q = append(q, "to="+f["to"])
	}
	if len(q) > 0 {
		path += "?" + strings.Join(q, "&")
	}
	return cmdGet(path)
}

func defaultNodeID(id string) (string, error) {
	if id != "" {
		return id, nil
	}
	raw, err := do("GET", "/api/v1/nodes", nil, true)
	if err != nil {
		return "", err
	}
	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil || len(listed.Items) == 0 {
		return "", fmt.Errorf("no local node is enrolled")
	}
	return listed.Items[0].ID, nil
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
