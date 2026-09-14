package ndnet

import "testing"

func TestProcHexIPv4(t *testing.T) {
	if got := procHexIPv4("0102A8C0"); got != "192.168.2.1" {
		t.Fatalf("gateway=%q", got)
	}
	if got := procHexIPv4("00000000"); got != "" {
		t.Fatalf("unspecified gateway=%q", got)
	}
}

func TestNormalizeMACRejectsMulticastAndZero(t *testing.T) {
	if got := normalizeMAC("34:5A:60:6A:03:1F"); got != "34:5a:60:6a:03:1f" {
		t.Fatalf("mac=%q", got)
	}
	if got := normalizeMAC("01:00:5e:00:00:01"); got != "" {
		t.Fatalf("multicast=%q", got)
	}
	if got := normalizeMAC("00:00:00:00:00:00"); got != "" {
		t.Fatalf("zero=%q", got)
	}
}
