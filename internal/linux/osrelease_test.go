package linux

import (
	"strings"
	"testing"
)

func TestParseOSReleaseDebian13(t *testing.T) {
	in := `PRETTY_NAME="Debian GNU/Linux 13 (trixie)"
NAME="Debian GNU/Linux"
VERSION_ID="13"
ID=debian
HOME_URL="https://www.debian.org/"
`
	got, err := ParseOSRelease(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "debian" {
		t.Fatalf("ID=%q", got.ID)
	}
	if got.VersionID != "13" {
		t.Fatalf("VERSION_ID=%q", got.VersionID)
	}
}

func TestParseOSReleaseMissingID(t *testing.T) {
	_, err := ParseOSRelease(strings.NewReader("VERSION_ID=13\n"))
	if err == nil {
		t.Fatal("expected error")
	}
}
