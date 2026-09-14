package oci

import "testing"

func TestValidateRegistryURL(t *testing.T) {
	if err := ValidateRegistryURL("https://registry.example", false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRegistryURL("http://127.0.0.1:5000", true); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRegistryURL("http://127.0.0.1:5000", false); err == nil {
		t.Fatal("http without insecure")
	}
	if err := ValidateRegistryURL("", false); err == nil {
		t.Fatal("empty")
	}
	if err := ValidateRegistryURL("https://", false); err == nil {
		t.Fatal("empty host")
	}
	if err := ValidateRegistryURL("file:///etc/passwd", false); err == nil {
		t.Fatal("file")
	}
	if err := ValidateRegistryURL("https://u:s3cret@registry.example", false); err == nil {
		t.Fatal("userinfo")
	}
}
