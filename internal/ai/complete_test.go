package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateEndpoint(t *testing.T) {
	if err := ValidateEndpoint(KindLocal, ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEndpoint(KindCompatible, "http://127.0.0.1:9/v1/chat/completions"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEndpoint(KindCompatible, "file:///etc/passwd"); err == nil {
		t.Fatal("file")
	}
	if err := ValidateEndpoint(KindCompatible, "https://sk-secret:x@api.example/v1"); err == nil {
		t.Fatal("userinfo")
	}
}

func TestHTTPCompleterDoesNotFollowRedirect(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "followed"}}},
		})
	}))
	defer final.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer ts.Close()
	text, err := (HTTPCompleter{}).Complete(context.Background(), CompleteRequest{
		Kind: KindCompatible, Endpoint: ts.URL, Prompt: "hi",
	})
	if err == nil || text != "" {
		t.Fatalf("redirect must not be followed: %q %v", text, err)
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("got %v", err)
	}
}
