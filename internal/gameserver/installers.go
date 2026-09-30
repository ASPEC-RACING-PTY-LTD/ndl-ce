package gameserver

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// installer is one reusable provisioning method. Templates name it through
// Template.InstallBuiltin. Native runs on the control plane with checksum,
// retry and resume support. Script is the container plan used when native
// install is unavailable (tests replace the runner) or when the method needs
// a toolchain image (SteamCMD, Java-based installers).
type installer struct {
	Name     string
	Label    string
	Kind     string
	Native   func(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error
	Script   func(t Template) (script, image string)
	Validate func(t Template) error
	// Update describes how Reinstall refreshes files. Worlds and configs are
	// never deleted by an install.
	Update string
	// RuntimeImageInstall runs the script in the server's selected runtime
	// image (for example the chosen Java version) instead of the plan image.
	RuntimeImageInstall bool
}

var installerRegistry = map[string]installer{}

func registerInstaller(i installer) {
	if _, dup := installerRegistry[i.Name]; dup {
		panic("duplicate installer " + i.Name)
	}
	installerRegistry[i.Name] = i
}

func lookupInstaller(name string) (installer, bool) {
	i, ok := installerRegistry[strings.ToLower(strings.TrimSpace(name))]
	return i, ok
}

// InstallerNames lists every registered installer. Used by validation tests
// and the API so the UI can offer a method filter.
func InstallerNames() []string {
	out := make([]string, 0, len(installerRegistry))
	for k := range installerRegistry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// UpdateProcedure explains how an update (Reinstall) behaves for a template.
func (t Template) UpdateProcedure() string {
	if inst, ok := lookupInstaller(t.InstallBuiltin); ok && inst.Update != "" {
		return inst.Update
	}
	return "Reinstall re-runs the installer. A rollback backup is taken first."
}

const (
	updateSteam    = "Reinstall runs SteamCMD app_update with validate. Saves and configs in the server folder are kept. A rollback backup is taken first."
	updateDownload = "Reinstall downloads the selected version again over the existing files. Saves and configs are kept. A rollback backup is taken first."
	updateJar      = "Change the version field, then Reinstall. The server jar is replaced; worlds, plugins and mods are kept. A rollback backup is taken first."
	updateImage    = "The server ships in its container image. Pull a newer image tag by changing the image, then restart."
)

func init() {
	registerInstaller(installer{Name: "steamcmd", Label: "SteamCMD", Kind: "SteamCMD", Script: steamcmdPlan, Validate: validateSteamTemplate, Update: updateSteam})
	registerInstaller(installer{Name: "paper", Label: "PaperMC API", Kind: "Java", Native: nativePaper, Script: fixedScript(paperInstallScript), Update: updateJar})
	registerInstaller(installer{Name: "minecraft-vanilla", Label: "Mojang launcher meta", Kind: "Java", Native: nativeVanilla, Script: fixedScript(vanillaInstallScript), Update: updateJar})
	registerInstaller(installer{Name: "minecraft-fabric", Label: "Fabric meta", Kind: "Java", Native: nativeFabric, Script: fixedScript(fabricInstallScript), Update: updateJar})
	registerInstaller(installer{Name: "minecraft-purpur", Label: "Purpur API", Kind: "Java", Native: nativePurpur, Script: fixedScript(purpurInstallScript), Update: updateJar})
	registerInstaller(installer{Name: "minecraft-velocity", Label: "PaperMC API", Kind: "Java", Native: nativeVelocity, Script: fixedScript(velocityInstallScript), Update: updateJar})
	registerInstaller(installer{Name: "mindustry", Label: "GitHub release", Kind: "Java", Native: nativeMindustry, Script: fixedScript(mindustryInstallScript), Update: updateDownload})
	registerInstaller(installer{Name: "terraria", Label: "Official download", Kind: "Standalone", Native: nativeTerraria, Script: fixedScript(terrariaInstallScript), Update: updateDownload})
	registerInstaller(installer{Name: "factorio", Label: "Official download", Kind: "Standalone", Native: nativeFactorio, Script: fixedScript(factorioInstallScript), Update: updateDownload})
	registerInstaller(installer{Name: "fivem", Label: "Cfx.re artifacts", Kind: "Standalone", Native: nativeFiveM, Script: fixedScript(fivemInstallScript), Update: updateDownload})
	registerInstaller(installer{Name: "download", Label: "Official download", Kind: "Standalone", Native: nativeDownloads, Script: downloadsPlan, Validate: validateDownloadTemplate, Update: updateDownload})
	registerInstaller(installer{Name: "github-release", Label: "GitHub release", Kind: "Standalone", Native: nativeDownloads, Script: downloadsPlan, Validate: validateDownloadTemplate, Update: updateDownload})
	registerInstaller(installer{Name: "image", Label: "Container image", Kind: "Image", Native: nativeImageOnly, Script: fixedScript(imageOnlyScript), Update: updateImage})
}

func fixedScript(s string) func(Template) (string, string) {
	return func(Template) (string, string) { return s, "debian:bookworm-slim" }
}

func nativePaper(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	return r.installPaperNative(ctx, srv, dir)
}
func nativeVanilla(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	return r.installVanillaNative(ctx, srv, dir)
}
func nativeFabric(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	return r.installFabricNative(ctx, srv, dir)
}
func nativePurpur(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	return r.installPurpurNative(ctx, srv, dir)
}
func nativeVelocity(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	return r.installVelocityNative(ctx, srv, dir)
}
func nativeMindustry(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	return r.installMindustryNative(ctx, srv, dir)
}
func nativeTerraria(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	return r.installTerrariaNative(ctx, srv, dir)
}
func nativeFactorio(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	return r.installFactorioNative(ctx, srv, dir)
}
func nativeFiveM(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	return r.installFiveMNative(ctx, srv, dir)
}

func nativeImageOnly(r *Runtime, ctx context.Context, srv Server, t Template, dir string) error {
	r.appendLog(srv.ID, "no file install needed; the server ships in "+t.DefaultImage)
	return nil
}

const imageOnlyScript = `#!/bin/sh
set -e
mkdir -p /mnt/server
echo "no file install needed; the server ships in its container image"
`

var steamAppIDPattern = regexp.MustCompile(`^[0-9]{2,8}$`)

func validateSteamTemplate(t Template) error {
	v, ok := t.variable("SRCDS_APPID")
	if !ok || !steamAppIDPattern.MatchString(v.Default) {
		return fmt.Errorf("steamcmd template needs a numeric SRCDS_APPID default")
	}
	if v.Editable {
		return fmt.Errorf("SRCDS_APPID must not be user editable")
	}
	if t.Install != nil {
		// Windows-only servers would need Wine or Proton; No-DAL ships no
		// verified Wine runtime, so only native Linux depots are allowed.
		if p := t.Install.Platform; p != "" && p != "linux" {
			return fmt.Errorf("steam platform %q is not supported; only native Linux servers are allowed", p)
		}
		if c := t.Install.AppConfig; c != "" && !regexp.MustCompile(`^[0-9]+ [a-z_]+ [A-Za-z0-9_.-]+$`).MatchString(c) {
			return fmt.Errorf("steam app_config %q is not in \"appid key value\" form", c)
		}
		if b := t.Install.Beta; b != "" && !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(b) {
			return fmt.Errorf("steam beta %q is not valid", b)
		}
		if len(t.Install.Downloads) > 0 {
			if err := validateDownloadTemplate(t); err != nil {
				return fmt.Errorf("add-on downloads: %w", err)
			}
		}
	}
	return nil
}
