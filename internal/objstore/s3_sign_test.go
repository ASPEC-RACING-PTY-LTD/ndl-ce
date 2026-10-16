package objstore

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// verifySigV4 recomputes a request's signature from the headers it says it
// signed, as R2 and S3 do, and reports whether it matches.
func verifySigV4(t *testing.T, r *http.Request, body []byte, access, secret, region string) bool {
	t.Helper()
	auth := r.Header.Get("Authorization")
	i := strings.Index(auth, "SignedHeaders=")
	if i < 0 {
		return false
	}
	signed := strings.SplitN(auth[i+len("SignedHeaders="):], ",", 2)[0]
	when, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return false
	}
	re, _ := http.NewRequest(r.Method, "http://"+r.Host+r.URL.RequestURI(), bytes.NewReader(body))
	for _, h := range strings.Split(signed, ";") {
		if h == "host" {
			continue
		}
		if h == "x-amz-date" || h == "x-amz-content-sha256" {
			continue // signV4 sets these itself
		}
		re.Header.Set(h, r.Header.Get(h))
	}
	signV4(re, body, access, secret, region, "s3", when)
	return re.Header.Get("Authorization") == auth
}

// Every request of a multipart upload, the completion included, must carry
// a signature that matches what it sends. A header changed after signing
// fails at R2 with SignatureDoesNotMatch.
func TestMultipartRequestsAreSignedAsSent(t *testing.T) {
	const access, secret, region = "AKTEST", "secret-test", "auto"
	var bad []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !verifySigV4(t, r, body, access, secret, region) {
			bad = append(bad, r.Method+" "+r.URL.RawQuery)
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<Error><Code>SignatureDoesNotMatch</Code></Error>`))
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.RawQuery == "uploads=":
			_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>up/1+x=</UploadId></InitiateMultipartUploadResult>`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.RawQuery, "partNumber="):
			w.Header().Set("ETag", `"etag"`)
		case r.Method == http.MethodPost && strings.Contains(r.URL.RawQuery, "uploadId="):
			if r.Header.Get("Content-Type") != "application/xml" {
				t.Errorf("completion content type %q", r.Header.Get("Content-Type"))
			}
			_, _ = w.Write([]byte(`<CompleteMultipartUploadResult/>`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	tr := NewS3Transport(srv.URL, region, access, secret, "r2", srv.Client())
	data := bytes.Repeat([]byte("x"), PartSize+1024)
	if err := tr.putMultipart(context.Background(), "bucket", "backups/packs/p.pack", bytes.NewReader(data)); err != nil {
		t.Fatalf("multipart upload: %v (rejected: %v)", err, bad)
	}
	if len(bad) > 0 {
		t.Fatalf("requests with a signature that does not match: %v", bad)
	}
}
