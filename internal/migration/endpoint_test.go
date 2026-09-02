package migration

import "testing"

func TestParseHTTPEndpoint(t *testing.T) {
	u, err := ParseHTTPEndpoint("https://pve.example:8006")
	if err != nil || u.Host != "pve.example:8006" {
		t.Fatalf("%v %+v", err, u)
	}
	if _, err := ParseHTTPEndpoint("http://127.0.0.1:8006"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseHTTPEndpoint(""); err == nil {
		t.Fatal("empty")
	}
	if _, err := ParseHTTPEndpoint("https://"); err == nil {
		t.Fatal("empty host")
	}
	if _, err := ParseHTTPEndpoint("file:///etc/passwd"); err == nil {
		t.Fatal("file")
	}
	if _, err := ParseHTTPEndpoint("https://user:SECRET-TOKEN-VALUE@pve.example:8006"); err == nil {
		t.Fatal("userinfo")
	}
}
