package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/identity"
)

const joinUsage = "usage: nodalctl cluster join --url URL --token TOKEN [--fingerprint SHA256] [--advertise HOST[:PORT]] [--hostname HOST]"

// cmdClusterJoin joins this machine to a cluster as a worker. It always
// talks to --url, never to a local control socket, so a host that already
// runs No-DAL can join another. The control certificate is pinned with
// --fingerprint, which makes a self-signed control safe to join over the
// internet.
func cmdClusterJoin(args []string) error {
	f := parseFlags(args)
	if f["token"] == "" {
		return errors.New(joinUsage)
	}
	hostname := f["hostname"]
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	base := strings.TrimRight(f["url"], "/")
	if base == "" {
		base = strings.TrimRight(os.Getenv("NODAL_URL"), "/")
	}
	if base == "" {
		return fmt.Errorf("--url is required: the control node's address, as shown on its Cluster page")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("--url must be an http or https address, such as https://control.example:8443")
	}
	if u.Scheme == "http" && f["insecure-http"] == "" && !isPrivateHost(u.Hostname()) {
		return fmt.Errorf("refusing to send the join token over plain http; use the https address, or pass --insecure-http if this network is trusted")
	}
	body, err := json.Marshal(map[string]any{"token": f["token"], "hostname": hostname, "listen_addr": f["advertise"]})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", base+"/api/v1/cluster/join", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{TLSClientConfig: joinTLS(u.Hostname(), f["fingerprint"])}}
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
		ID         string `json:"id"`
		Name       string `json:"name"`
		ClusterID  string `json:"cluster_id"`
		CACert     string `json:"ca_cert"`
		NodeCert   string `json:"node_cert"`
		NodeKey    string `json:"node_key"`
		ListenAddr string `json:"listen_addr"`
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
	fmt.Printf("Joined as %s (node %s).\n", joined.Name, joined.ID)
	// The agent opens its mutual TLS port at start once it holds a node
	// certificate. Guests are their own units, so this does not stop them.
	if err := exec.Command("systemctl", "restart", "ndl-agent.service").Run(); err != nil {
		fmt.Println("Restart the agent to open its TLS port: sudo systemctl restart ndl-agent")
	}
	if joined.ListenAddr != "" {
		fmt.Printf("The control node reaches this agent at %s. Allow TCP %s in from the control node.\n", joined.ListenAddr, portOf(joined.ListenAddr))
	} else {
		fmt.Println("Set this node's agent address on the control node's Cluster page (port 9444).")
	}
	return nil
}

// joinTLS verifies the control certificate normally, and also accepts a
// self-signed one whose SHA-256 matches the pinned fingerprint.
func joinTLS(serverName, pin string) *tls.Config {
	pin = strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(strings.TrimSpace(pin)))
	if os.Getenv("NODAL_TLS_INSECURE") == "1" {
		return &tls.Config{InsecureSkipVerify: true}
	}
	if pin == "" {
		return &tls.Config{ServerName: serverName}
	}
	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("control node sent no certificate")
			}
			sum := sha256.Sum256(raw[0])
			if hex.EncodeToString(sum[:]) == pin {
				return nil
			}
			leaf, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return err
			}
			inter := x509.NewCertPool()
			for _, r := range raw[1:] {
				if c, err := x509.ParseCertificate(r); err == nil {
					inter.AddCert(c)
				}
			}
			if _, err := leaf.Verify(x509.VerifyOptions{DNSName: serverName, Intermediates: inter}); err != nil {
				return fmt.Errorf("control certificate does not match --fingerprint and is not publicly trusted: %w", err)
			}
			return nil
		},
	}
}

// isPrivateHost reports a LAN or loopback IP, where plain http stays on
// the local network.
func isPrivateHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func portOf(hostport string) string {
	if i := strings.LastIndex(hostport, ":"); i >= 0 {
		return hostport[i+1:]
	}
	return "9444"
}
