package docker

import (
	"net/http"
	"testing"
)

// A new client per inventory pass leaked its idle connections and ran the
// agent out of file descriptors. One client per socket is reused, and its
// idle connections time out.
func TestDockerClientIsSharedPerSocketAndIdleConnectionsExpire(t *testing.T) {
	e := &Engine{}
	a := e.client("/run/test-a/docker.sock")
	b := e.client("/run/test-a/docker.sock")
	if a != b {
		t.Fatal("the same socket must reuse one client")
	}
	if e.client("/run/test-b/docker.sock") == a {
		t.Fatal("each socket has its own client")
	}
	tr, ok := a.(*unixClient).http.Transport.(*http.Transport)
	if !ok || tr.IdleConnTimeout <= 0 {
		t.Fatalf("idle connections must close on their own: %+v", tr)
	}
}
