package gameserver

import (
	"context"
	"strings"
	"testing"
)

func TestSecretSettingsStayOffTheCommandLine(t *testing.T) {
	flags, secret := envArgs(map[string]string{
		"MAP": "gm_flatgrass", "STEAM_TOKEN": "ABCDEF0123456789ABCDEF0123456789", "RCON_PASSWORD": "hunter2", "WORKSHOP_COLLECTION": "",
	})
	joined := strings.Join(flags, " ")
	if strings.Contains(joined, "ABCDEF0123") || strings.Contains(joined, "hunter2") {
		t.Fatalf("a secret value reached the command line: %s", joined)
	}
	if !strings.Contains(joined, "-e STEAM_TOKEN") || !strings.Contains(joined, "-e RCON_PASSWORD") || !strings.Contains(joined, "-e MAP=gm_flatgrass") || !strings.Contains(joined, "-e WORKSHOP_COLLECTION=") {
		t.Fatalf("flags %s", joined)
	}
	if len(secret) != 2 || secret[0] != "RCON_PASSWORD=hunter2" || secret[1] != "STEAM_TOKEN=ABCDEF0123456789ABCDEF0123456789" {
		t.Fatalf("secret env %q", secret)
	}
	// The value reaches the child through its environment.
	out, err := NewRuntime(t.TempDir()).runWithEnv(context.Background(), secret, "/bin/sh", "-c", `printf %s "$RCON_PASSWORD"`)
	if err != nil || string(out) != "hunter2" {
		t.Fatalf("child env %q %v", out, err)
	}
}
