package cluster

import (
	"crypto/tls"
	"net"
	"testing"
	"time"
)

// handshake runs one TLS handshake between a worker agent config and a
// client config and returns the client's error.
func handshake(t *testing.T, server, client *tls.Config) error {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", server)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = c.(*tls.Conn).Handshake()
		_, _ = c.Write([]byte("x"))
		_ = c.Close()
	}()
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", ln.Addr().String(), client)
	if err != nil {
		return err
	}
	defer c.Close()
	// TLS 1.3 reports a rejected client certificate on the first read.
	_, err = c.Read(make([]byte, 1))
	return err
}

func workerServer(t *testing.T, ca CA, nodeID string) *tls.Config {
	t.Helper()
	certPEM, keyPEM, err := ca.IssueNode(nodeID, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	caPEM, _ := ca.CertPEM()
	cfg, err := AgentServerTLS(caPEM, certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestAgentMTLSAcceptsControlForItsNode(t *testing.T) {
	ca := CA{Dir: t.TempDir()}
	server := workerServer(t, ca, "node-a")
	client, err := ca.AgentClientTLS("node-a", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, server, client); err != nil {
		t.Fatalf("control must reach its worker: %v", err)
	}
}

func TestAgentMTLSRejectsWrongNode(t *testing.T) {
	ca := CA{Dir: t.TempDir()}
	server := workerServer(t, ca, "node-a")
	client, err := ca.AgentClientTLS("node-b", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, server, client); err == nil {
		t.Fatal("control must not accept another node's certificate")
	}
}

func TestAgentMTLSRejectsRevokedWorker(t *testing.T) {
	ca := CA{Dir: t.TempDir()}
	server := workerServer(t, ca, "node-a")
	if err := ca.RevokeNode("node-a"); err != nil {
		t.Fatal(err)
	}
	client, _ := ca.AgentClientTLS("node-a", time.Time{})
	if err := handshake(t, server, client); err == nil {
		t.Fatal("a revoked worker must not be dialed")
	}
}

func TestAgentMTLSRejectsNodeCertAsCaller(t *testing.T) {
	ca := CA{Dir: t.TempDir()}
	server := workerServer(t, ca, "node-a")
	// Another worker's certificate is valid for the cluster but is not the control plane.
	certPEM, keyPEM, err := ca.IssueNode("node-b", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := ca.AgentClientTLS("node-a", time.Time{})
	client.Certificates = []tls.Certificate{peer}
	if err := handshake(t, server, client); err == nil {
		t.Fatal("a worker certificate must not be able to drive another agent")
	}
}

func TestAgentMTLSRejectsNoClientCert(t *testing.T) {
	ca := CA{Dir: t.TempDir()}
	server := workerServer(t, ca, "node-a")
	client, _ := ca.AgentClientTLS("node-a", time.Time{})
	client.Certificates = nil
	if err := handshake(t, server, client); err == nil {
		t.Fatal("the agent port must refuse callers without a certificate")
	}
}

func TestAgentMTLSRejectsForeignCluster(t *testing.T) {
	server := workerServer(t, CA{Dir: t.TempDir()}, "node-a")
	other := CA{Dir: t.TempDir()}
	client, err := other.AgentClientTLS("node-a", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, server, client); err == nil {
		t.Fatal("another cluster's control plane must be refused")
	}
}

func TestControlCertificateIsReused(t *testing.T) {
	ca := CA{Dir: t.TempDir()}
	a, err := ca.ControlCertificate(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ca.ControlCertificate(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if string(a.Certificate[0]) != string(b.Certificate[0]) {
		t.Fatal("the control certificate must be kept, not reissued every call")
	}
}
