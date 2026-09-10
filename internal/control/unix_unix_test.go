//go:build unix

package control

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestListenControlUnixMode0600(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "control.sock")
	ln, err := listenControlUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	st, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
}

func TestAppendLocalControlSocketSkipsOnBindError(t *testing.T) {
	instances := appendLocalControlSocket("/proc/1/fd/0/not-a-socket", http.NotFoundHandler(), nil)
	if len(instances) != 0 {
		t.Fatalf("%d", len(instances))
	}
}
