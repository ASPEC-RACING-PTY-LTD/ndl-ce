package objstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListPaginatesWithContinuationToken(t *testing.T) {
	var paths, tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("list-type") != "2" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		paths = append(paths, r.URL.Path)
		tokens = append(tokens, r.URL.Query().Get("continuation-token"))
		if r.URL.Query().Get("prefix") != "backups/" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		if r.URL.Query().Get("continuation-token") == "" {
			_, _ = w.Write([]byte(`<ListBucketResult><Contents><Key>backups/packs/a.pack</Key><Size>10</Size></Contents>` +
				`<IsTruncated>true</IsTruncated><NextContinuationToken>tok/+=1</NextContinuationToken></ListBucketResult>`))
			return
		}
		_, _ = w.Write([]byte(`<ListBucketResult><Contents><Key>backups/x/manifests/b.snap</Key><Size>5</Size></Contents>` +
			`<IsTruncated>false</IsTruncated></ListBucketResult>`))
	}))
	defer srv.Close()
	s := NewS3Transport(srv.URL, "auto", "ak", "sk", KindR2, srv.Client())
	got, err := s.List(context.Background(), "ndl-ce", "backups/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "backups/packs/a.pack" || got[0].Size != 10 || got[1].Size != 5 {
		t.Fatalf("listing %+v", got)
	}
	if len(paths) != 2 || paths[0] != "/ndl-ce" {
		t.Fatalf("bucket-level request path %v", paths)
	}
	if tokens[1] != "tok/+=1" {
		t.Fatalf("continuation token must round-trip, got %q", tokens[1])
	}
}

func TestListReportsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code></Error>"))
	}))
	defer srv.Close()
	s := NewS3Transport(srv.URL, "auto", "ak", "sk", KindR2, srv.Client())
	if _, err := s.List(context.Background(), "b", ""); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected a 403 error, got %v", err)
	}
}
