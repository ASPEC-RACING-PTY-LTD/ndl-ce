package agentrpc

import (
	"testing"
	"time"
)

func TestRPCClientIsReused(t *testing.T) {
	c := Client{Socket: "/tmp/ndl-rpc-reuse.sock"}
	a := c.rpc()
	b := c.rpc()
	if a != b {
		t.Fatal("rpc client must be reused for the same socket")
	}
	other := Client{Socket: "/tmp/ndl-rpc-reuse-other.sock"}.rpc()
	if a == other {
		t.Fatal("different sockets must not share a client")
	}
	tcp := Client{TCPAddr: "127.0.0.1:9"}.rpc()
	if tcp == a {
		t.Fatal("tcp and unix clients must be distinct")
	}
	if (Client{TCPAddr: "127.0.0.1:9"}).rpc() != tcp {
		t.Fatal("tcp client must be reused for the same address")
	}
}

func TestTerminalRPCDoesNotShareUnaryConnection(t *testing.T) {
	c := Client{Socket: "/tmp/ndl-term-vs-unary.sock"}
	if c.rpc() == c.streamRPC() {
		t.Fatal("terminal attach must not share the unary HTTP/2 connection")
	}
	if c.streamRPC() != c.streamRPC() {
		t.Fatal("terminal connection must be reused for the same socket")
	}
}

func TestTerminalH2HasNoReadIdlePing(t *testing.T) {
	tr := newAgentH2("unix", "/tmp/ndl-term-h2.sock", 0, 0)
	if tr.ReadIdleTimeout != 0 {
		t.Fatalf("terminal ReadIdleTimeout = %s, idle pings drop apt and docker compose sessions", tr.ReadIdleTimeout)
	}
	unary := newAgentH2("unix", "/tmp/ndl-term-h2.sock", rpcReadIdleTimeout, rpcPingTimeout)
	if unary.ReadIdleTimeout != 30*time.Second || unary.PingTimeout != 15*time.Second {
		t.Fatalf("unary health check changed: idle %s ping %s", unary.ReadIdleTimeout, unary.PingTimeout)
	}
}
