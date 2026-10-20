package guestextras

import (
	"slices"
	"testing"
)

func TestResolveComposeRequiresDocker(t *testing.T) {
	got := Resolve([]string{Compose})
	if !slices.Equal(got, []string{Docker, Compose}) {
		t.Fatalf("compose resolve %v", got)
	}
}

func TestNeedsNesting(t *testing.T) {
	if !NeedsNesting([]string{Compose}) {
		t.Fatal("compose must enable nesting")
	}
	if NeedsNesting([]string{Git, Curl}) {
		t.Fatal("git/curl must not force nesting")
	}
}

func TestAvailabilityByFamily(t *testing.T) {
	debianDocker := AvailabilityFor("debian/trixie/amd64/default", Docker)
	if !debianDocker.Available || debianDocker.Packages[0] != "docker.io" {
		t.Fatalf("debian docker %+v", debianDocker)
	}
	alpineCompose := AvailabilityFor("alpine/3.21/amd64/default", Compose)
	if !alpineCompose.Available || alpineCompose.Packages[0] != "docker-cli-compose" {
		t.Fatalf("alpine compose %+v", alpineCompose)
	}
	debianCompose := AvailabilityFor("debian/trixie/amd64/default", Compose)
	if !debianCompose.Available || !slices.Contains(debianCompose.Packages, "docker-compose") || !slices.Contains(debianCompose.Packages, "docker-compose-v2") {
		t.Fatalf("debian compose must try both suite package names, got %+v", debianCompose)
	}
	debianGH := AvailabilityFor("debian/bookworm/amd64/default", GitHubCLI)
	if debianGH.Available {
		t.Fatal("GitHub CLI must be unavailable on Debian default repos")
	}
	unknown := AvailabilityFor("fedora/42/amd64/default", Docker)
	if unknown.Available {
		t.Fatal("unknown family must be unavailable")
	}
}

func TestResolveDropsUnknown(t *testing.T) {
	got := Resolve([]string{"not-real", Git})
	if !slices.Equal(got, []string{Git}) {
		t.Fatalf("got %v", got)
	}
}

func TestBaselineExtras(t *testing.T) {
	base := BaselineIDs()
	for _, id := range []string{Curl, Wget, Jq, CACertificates, Unzip, Zip} {
		if !IsBaseline(id) {
			t.Fatalf("%s should be a baseline extra", id)
		}
	}
	if IsBaseline(Docker) || IsBaseline("nope") {
		t.Fatal("only the basic tools are baseline")
	}
	got := WithBaseline([]string{Compose})
	for _, id := range append(base, Docker, Compose) {
		found := false
		for _, g := range got {
			found = found || g == id
		}
		if !found {
			t.Fatalf("WithBaseline lost %s: %v", id, got)
		}
	}
	if av := AvailabilityFor("alpine/3.21/amd64/default", Cloudflared); av.Available {
		t.Fatal("cloudflared is Debian only")
	}
	if av := AvailabilityFor("debian/trixie/amd64/default", Cloudflared); !av.Available {
		t.Fatalf("cloudflared on Debian: %+v", av)
	}
}
