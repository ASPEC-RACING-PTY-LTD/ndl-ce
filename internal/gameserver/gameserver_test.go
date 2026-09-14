package gameserver

import (
	"context"
	"strings"
	"testing"
)

func TestParseEggJSONNormalizesWithoutCopyingRaw(t *testing.T) {
	raw := []byte(`{
	  "name": "Paper",
	  "description": "Minecraft Paper",
	  "features": ["eula", "java_version"],
	  "docker_images": {"Java 21": "ghcr.io/example/yolks:java_21"},
	  "startup": "java -jar {{SERVER_JARFILE}}",
	  "scripts": {"installation": {"script": "echo hi", "container": "alpine:3.21"}},
	  "variables": [
	    {"name": "EULA", "env_variable": "EULA", "default_value": "false", "user_viewable": true, "user_editable": true, "rules": "required|boolean"},
	    {"name": "Token", "env_variable": "STEAM_TOKEN", "default_value": "secretvalue", "user_viewable": true, "user_editable": true}
	  ]
	}`)
	tmpl, err := ParseEggJSON(raw, "https://example.com/paper.json")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Game != "minecraft" || tmpl.Implementation != "paper" {
		t.Fatalf("identity %+v", tmpl)
	}
	if !hasCap(tmpl.Capabilities, CapPlugins) || !hasCap(tmpl.Capabilities, CapEULA) {
		t.Fatalf("caps %v", tmpl.Capabilities)
	}
	found := false
	for _, v := range tmpl.Variables {
		if v.Env == "STEAM_TOKEN" {
			found = true
			if !v.Secret || v.Viewable {
				t.Fatalf("secret var leaked %+v", v)
			}
		}
	}
	if !found {
		t.Fatal("token variable missing")
	}
}

func TestPropertiesLosslessUnknownLines(t *testing.T) {
	raw := "# heading\nmotd=Hello\nweird no equals\nmax-players=20\n"
	f := ParseProperties(raw)
	f.Set("motd", "No-DAL")
	out := f.Bytes()
	if !strings.Contains(out, "# heading") || !strings.Contains(out, "weird no equals") {
		t.Fatalf("lost unknown lines: %q", out)
	}
	if !strings.Contains(out, "motd=No-DAL") || !strings.Contains(out, "max-players=20") {
		t.Fatalf("lost known keys: %q", out)
	}
}

func TestValidatePublicURLRejectsPrivate(t *testing.T) {
	if err := ValidatePublicURL("http://127.0.0.1/egg.json"); err == nil {
		t.Fatal("loopback allowed")
	}
	if err := ValidatePublicURL("file:///etc/passwd"); err == nil {
		t.Fatal("file url allowed")
	}
}

func TestHumanErrorKnownFailures(t *testing.T) {
	if !strings.Contains(HumanError("eula not accepted"), "EULA") {
		t.Fatal("eula")
	}
	if !strings.Contains(strings.ToLower(HumanError("cannot connect to the docker daemon")), "docker") {
		t.Fatal("docker")
	}
	if strings.Contains(HumanError("exit code 1 and something unique"), "exit code 1 and something unique") {
		t.Fatal("should wrap exit code")
	}
}

func TestRedactEnvHidesSecrets(t *testing.T) {
	out := RedactEnv(map[string]string{"SERVER_NAME": "fun", "FIVEM_LICENSE": "abcdef", "STEAM_TOKEN": "tok"})
	if out["SERVER_NAME"] != "fun" || out["FIVEM_LICENSE"] != redacted || out["STEAM_TOKEN"] != redacted {
		t.Fatalf("%v", out)
	}
	if got := RedactLog("token is abcdef", map[string]string{"FIVEM_LICENSE": "abcdef"}); strings.Contains(got, "abcdef") {
		t.Fatalf("%q", got)
	}
}

func TestSafeExtractRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := SafeExtract(dir, "../escape", []byte("hi"), "a.txt"); err == nil {
		t.Fatal("traversal dest allowed")
	}
}

func TestExpandStartupSubstitutes(t *testing.T) {
	got := ExpandStartup("java -Xmx{{SERVER_MEMORY}}M -p ${SERVER_PORT}", map[string]string{"SERVER_JARFILE": "server.jar"}, 1536, 25565)
	if !strings.Contains(got, "1536") || !strings.Contains(got, "25565") {
		t.Fatalf("%s", got)
	}
}

func TestBuiltinTemplatesHaveCapabilities(t *testing.T) {
	tmpls := BuiltinTemplates()
	if len(tmpls) < 24 || len(tmpls) > 28 {
		t.Fatalf("want about 25 builtins, got %d", len(tmpls))
	}
	seen := map[string]bool{}
	for _, tmpl := range tmpls {
		if seen[tmpl.ID] {
			t.Fatalf("duplicate %s", tmpl.ID)
		}
		seen[tmpl.ID] = true
		if len(tmpl.Capabilities) == 0 || tmpl.DefaultImage == "" || tmpl.Startup == "" {
			t.Fatalf("%s incomplete", tmpl.ID)
		}
		if len(tmpl.DefaultPorts) == 0 {
			t.Fatalf("%s has no ports", tmpl.ID)
		}
		script, image := installPlan(tmpl)
		if script == "" || image == "" {
			t.Fatalf("%s has no installer", tmpl.ID)
		}
		if err := validateEnv(tmpl, mergeEnv(tmpl, nil)); err != nil {
			t.Fatalf("%s default env: %v", tmpl.ID, err)
		}
		for _, key := range tmpl.StartRequires {
			if _, ok := tmpl.variable(key); !ok {
				t.Fatalf("%s StartRequires %s is not a variable", tmpl.ID, key)
			}
		}
		if !hasCap(tmpl.Capabilities, CapDatabases) {
			continue
		}
		t.Fatalf("%s claimed databases without a manager", tmpl.ID)
	}
	for _, id := range []string{"ndl-minecraft-paper", "ndl-valheim", "ndl-fivem", "ndl-gmod", "ndl-mindustry"} {
		if !seen[id] {
			t.Fatalf("missing required builtin %s", id)
		}
	}
}

func TestNetworkArgsHostSkipsPublish(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	rt.NetworkMode = "host"
	args := rt.networkArgs()
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--network host") {
		t.Fatalf("args %v", args)
	}
	if strings.Contains(joined, "--dns") {
		t.Fatal("host network should use the host resolver")
	}
	var sawPublish bool
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		for i := range args {
			if args[i] == "-p" {
				sawPublish = true
			}
		}
		return []byte("cid"), nil
	}
	if _, err := rt.Start(t.Context(), RunSpec{
		ID: "n1", Image: "alpine:3.21", Ports: []Port{{ContainerPort: 25565, HostPort: 25565, Protocol: "tcp"}},
	}); err != nil {
		t.Fatal(err)
	}
	if sawPublish {
		t.Fatal("host network must not publish ports")
	}
}

func TestInstallOverridesImageEntrypoint(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	var joined string
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined = strings.Join(args, " ")
		return []byte("ok"), nil
	}
	tmpl, _ := templateByID("ndl-valheim")
	if err := rt.Install(t.Context(), Server{ID: "e1", Env: map[string]string{"SRCDS_APPID": "896660"}}, tmpl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joined, "--entrypoint /bin/sh") {
		t.Fatalf("missing entrypoint override: %s", joined)
	}
}

func TestPickFiveMArtifactPrefersPrimary(t *testing.T) {
	html := `<a href="./111-aaa/fx.tar.xz"></a><a href="./222-bbb/fx.tar.xz" class="button is-link is-primary">`
	if got := pickFiveMArtifact(html); got != "222-bbb" {
		t.Fatalf("got %q", got)
	}
}

func TestHumanErrorFiveMLicense(t *testing.T) {
	if !strings.Contains(HumanError("fivem license key is required"), "Cfx.re") {
		t.Fatal(HumanError("fivem license key is required"))
	}
	if !strings.Contains(HumanError("klei cluster token is required"), "Klei") {
		t.Fatal(HumanError("klei cluster token is required"))
	}
}
