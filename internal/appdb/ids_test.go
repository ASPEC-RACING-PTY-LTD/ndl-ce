package appdb

import "testing"

func TestValidUUID(t *testing.T) {
	if ValidUUID("") || ValidUUID("   ") || ValidUUID("system") || ValidUUID("not-a-uuid") {
		t.Fatal("empty and malformed values must not be treated as UUIDs")
	}
	if !ValidUUID("5248e1c6-7df9-4bdc-8dd9-ba1d7d524090") {
		t.Fatal("valid UUID rejected")
	}
}
