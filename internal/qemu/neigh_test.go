package qemu

import "testing"

func TestIPv4ForMACTableIgnoresIncomplete(t *testing.T) {
	table := []byte(`IP address       HW type     Flags       HW address            Mask     Device
192.168.2.1      0x1         0x2         1e:0b:8b:46:04:ae     *        ndl685ff937
192.168.2.183    0x1         0x2         0e:68:ba:17:4b:1b     *        ndl685ff937
10.1.1.9         0x1         0x0         0e:68:ba:17:4b:1b     *        ndl685ff937
`)
	if got := IPv4ForMACTable("0E-68-BA-17-4B-1B", table); got != "192.168.2.183" {
		t.Fatalf("got %q", got)
	}
	if IPv4ForMACTable("aa:bb:cc:dd:ee:ff", table) != "" {
		t.Fatal("unknown MAC must be empty")
	}
	if IPv4ForMACTable("", table) != "" {
		t.Fatal("empty MAC must be empty")
	}
}
