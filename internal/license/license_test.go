package license

import (
	"context"
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
