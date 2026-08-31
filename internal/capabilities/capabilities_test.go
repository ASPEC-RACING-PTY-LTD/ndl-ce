package capabilities

import "testing"

func TestDefaultEnabledEmpty(t *testing.T) {
	if n := len(DefaultEnabled()); n != 0 {
		t.Fatalf("enabled=%d", n)
	}
}
