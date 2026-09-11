package objstore

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestQuoteETagAndCompleteXML(t *testing.T) {
	if quoteETag(`abc`) != `"abc"` {
		t.Fatal("etag must be quoted")
	}
	if quoteETag(`"abc"`) != `"abc"` {
		t.Fatal("already quoted etag must not double-quote")
	}
	xml := string(completeMultipartXML([]partMarker{
		{PartNumber: 1, ETag: `abc`},
		{PartNumber: 2, ETag: `"def"`},
	}))
	if strings.Contains(xml, "&quot;") {
		t.Fatalf("complete XML must not entity-escape quotes: %s", xml)
	}
	if !strings.Contains(xml, `<ETag>"abc"</ETag>`) || !strings.Contains(xml, `<ETag>"def"</ETag>`) {
		t.Fatalf("complete XML %s", xml)
	}
}

func TestPutStreamMultipartQuotesETagsAndAborts(t *testing.T) {
	var mu sync.Mutex
	var completeBody []byte
	aborted := false
	peakPart := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.RawQuery == "uploads=":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>up-1</UploadId></InitiateMultipartUploadResult>`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.RawQuery, "partNumber="):
			n, _ := io.Copy(io.Discard, r.Body)
			if int(n) > peakPart {
				peakPart = int(n)
			}
			w.Header().Set("ETag", `"part-etag"`)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.Contains(r.URL.RawQuery, "uploadId="):
			mu.Lock()
			completeBody, _ = io.ReadAll(r.Body)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<CompleteMultipartUploadResult></CompleteMultipartUploadResult>`))
		case r.Method == http.MethodDelete && strings.Contains(r.URL.RawQuery, "uploadId="):
			aborted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	tr := NewS3Transport(srv.URL, "auto", "ak", "sk", KindMinIO, srv.Client())
	payload := bytes.Repeat([]byte("n"), PartSize+1024)
	if err := tr.PutStream(context.Background(), "ndl-ce", "backups/SoundDock/stamp/chunks/000000", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	body := string(completeBody)
	mu.Unlock()
	if strings.Contains(body, "&quot;") {
		t.Fatalf("complete body escaped quotes: %s", body)
	}
	if !strings.Contains(body, `<ETag>"part-etag"</ETag>`) {
		t.Fatalf("complete body %s", body)
	}
	if peakPart > PartSize {
		t.Fatalf("part buffer %d exceeds PartSize", peakPart)
	}
	if aborted {
		t.Fatal("successful upload must not abort")
	}

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.RawQuery == "uploads=":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>up-2</UploadId></InitiateMultipartUploadResult>`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.RawQuery, "partNumber="):
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
		case r.Method == http.MethodDelete:
			aborted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(failSrv.Close)
	aborted = false
	tr = NewS3Transport(failSrv.URL, "auto", "ak", "sk", KindMinIO, failSrv.Client())
	err := tr.PutStream(context.Background(), "ndl-ce", "k", bytes.NewReader(payload), int64(len(payload)))
	if err == nil {
		t.Fatal("forbidden part must fail")
	}
	if !aborted {
		t.Fatal("failed multipart must abort")
	}
}
