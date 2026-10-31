package codeholddl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchFollowsLatestToOneRelease(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/downloads/latest/SHA256SUMS":
			http.Redirect(w, r, "/downloads/1.0.0/SHA256SUMS", http.StatusFound)
		case "/downloads/1.0.0/SHA256SUMS":
			_, _ = w.Write([]byte(sum + "  ch-linux-amd64\n" + strings.Repeat("cd", 32) + " *codehold-runner-linux-amd64\nnot a line\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	old := Client
	Client = ts.Client()
	defer func() { Client = old }()

	rel, err := fetch(context.Background(), ts.URL+"/downloads/latest/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Dir != ts.URL+"/downloads/1.0.0" || len(rel.Sums) != 2 {
		t.Fatalf("%+v", rel)
	}
	u, s, err := rel.File(CLIName("amd64"))
	if err != nil || u != ts.URL+"/downloads/1.0.0/ch-linux-amd64" || s != sum {
		t.Fatalf("%q %q %v", u, s, err)
	}
	if _, _, err := rel.File(CLIName("arm64")); err == nil {
		t.Fatal("a file the release does not list must be refused")
	}
	if _, err := fetch(context.Background(), ts.URL+"/downloads/missing/SHA256SUMS"); err == nil {
		t.Fatal("a missing release must fail")
	}
}
