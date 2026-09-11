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
