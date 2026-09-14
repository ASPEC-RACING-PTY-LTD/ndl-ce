package agentrpc

import "testing"

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
