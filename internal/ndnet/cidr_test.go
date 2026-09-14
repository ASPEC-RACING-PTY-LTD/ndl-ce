package ndnet

import "testing"

func TestSameIPv4Net(t *testing.T) {
	if !SameIPv4Net("10.77.0.0/24", "10.77.0.0/24") {
		t.Fatal("identical CIDRs must match")
	}
	if SameIPv4Net("10.77.0.0/24", "10.77.40.0/24") {
		t.Fatal("distinct isolated CIDRs must not match")
	}
	if SameIPv4Net("not-a-cidr", "10.77.0.0/24") {
		t.Fatal("invalid CIDR must not match")
	}
}
