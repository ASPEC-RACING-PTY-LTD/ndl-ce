package license

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLast4DoesNotReturnShortKeys(t *testing.T) {
	if Last4("") != "" {
		t.Fatal("empty")
	}
	if Last4("short") != "****" {
		t.Fatal("short")
	}
	if Last4("enterprise-key-value") != "alue" {
		t.Fatalf("%s", Last4("enterprise-key-value"))
	}
}

func TestHTTPProbeEmptyKeyDoesNotContactAPI(t *testing.T) {
	if err := (HTTPProbe{}).Check(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPProbeRequiresAcceptedEntitlement(t *testing.T) {
	secret := "EE-SECRET-LICENSE-VALUE"
	var sawBody []byte
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawBody, _ = io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/key-only":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"key":"EE-SECRET-LICENSE-VALUE"}`))
		case "/accepted-false":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"accepted":false}`))
		case "/accepted":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"accepted":true}`))
		case "/signed":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"signature":"signed-looking-value"}`))
		case "/empty":
			w.WriteHeader(http.StatusOK)
		case "/fail":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"accepted":true}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	probe := HTTPProbe{Client: srv.Client()}
	cases := []struct {
		path string
		want error
	}{
		{path: "/ok", want: ErrNotEntitled},
		{path: "/key-only", want: ErrNotEntitled},
		{path: "/accepted-false", want: ErrNotEntitled},
		{path: "/empty", want: ErrNotEntitled},
		{path: "/", want: ErrNotEntitled},
		{path: "/fail", want: ErrUnreachable},
		{path: "/accepted", want: nil},
		{path: "/signed", want: nil},
	}
	for _, tc := range cases {
		probe.Endpoint = srv.URL + tc.path
		err := probe.Check(context.Background(), secret)
		if tc.want == nil {
			if err != nil {
				t.Fatalf("%s: %v", tc.path, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.path, err, tc.want)
		}
	}
	if sawAuth != "Bearer "+secret {
		t.Fatalf("auth %q", sawAuth)
	}
	if strings.Contains(string(sawBody), secret) || strings.Contains(string(sawBody), `"key"`) {
		t.Fatalf("key in JSON %s", sawBody)
	}
	var body map[string]any
	if err := json.Unmarshal(sawBody, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["key"]; ok {
		t.Fatal("request JSON must not include key")
	}
}

func TestHTTPProbeUnreachableHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()
	err := (HTTPProbe{Client: srv.Client(), Endpoint: srv.URL}).Check(context.Background(), "license-key-value")
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("got %v", err)
	}
}
