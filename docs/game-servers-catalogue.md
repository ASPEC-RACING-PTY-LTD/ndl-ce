# Game Servers catalogue

The Game Servers feature ships a builtin catalogue of templates in
`internal/gameserver`. Imported Pelican/Pterodactyl eggs are normalised
into the same `Template` shape and stored per cluster; builtins are code.

## Where things live

| File | Purpose |
|---|---|
| `template.go` | `Template`, `Variable`, `Port`, `InstallSpec`, `Download`, `Requirement` |
| `builtins*.go` | Builtin templates, one file per family |
| `builders.go` | Shared builders: `steamServer`, `srcdsTemplate`, `goldsrcTemplate`, `nativeServer`, `minecraftJava` |
| `installers.go` | Installer registry (`registerInstaller`) |
| `downloads.go` | `download` and `github-release` installers |
| `minecraft_installers.go` | PaperMC API, Forge/NeoForge/Quilt/Fabric, Spigot BuildTools, Sponge, Bedrock, Modrinth modpacks |
| `install.go` | Install flow, SteamCMD plan |
| `runtime.go`, `runtime_image.go` | Container start/stop, dependency images |
| `ports.go` | Port allocation and conflict checks |
| `validate.go` | `ValidateTemplate`, image allowlist, package allowlist |
| `verification.go` | Verification levels and recorded live test results |

Template IDs never change once shipped: servers resolve their template by
ID at every start. A template that turns out to be unusable is marked
`Hidden` (kept resolvable, removed from the catalogue) instead of deleted.

## Installers

| `InstallBuiltin` | What it does |
|---|---|
| `steamcmd` | SteamCMD `app_update ... validate`, 3 attempts, optional beta and `app_set_config`, anonymous unless the template requires an owning account. Copies `steamclient.so` to `$HOME/.steam/sdk32` and `sdk64`. May overlay `Install.Downloads` afterwards (mods such as a community server build). |
| `github-release` | Resolves the latest release or the tag in the version variable through the GitHub API, verifies the asset's published sha256 digest, extracts it. A pinned version may use a direct `releases/download` URL. |
| `download` | Official https download, optional `{{VAR}}` placeholders restricted to version-like values, optional published sha256 file. |
| `image` | The server ships inside its container image. |
| `paper`, `papermc` | PaperMC Fill API (Paper, Folia, Velocity), sha256 verified. |
| `minecraft-vanilla`, `minecraft-fabric`, `minecraft-purpur`, `minecraft-velocity` | Mojang launcher metadata (sha1 verified), Fabric meta, Purpur API (md5 verified). |
| `minecraft-loader` | Forge, NeoForge, Quilt or Fabric installer run in the server's selected Java image; writes `ndl-java-args.txt`. |
| `minecraft-spigot` | Spigot BuildTools in a JDK image. |
| `minecraft-sponge` | SpongeVanilla downloads API, sha1 verified. |
| `minecraft-bedrock` | Official Bedrock Dedicated Server; keeps `server.properties`, permissions and allowlist on update. |
| `modrinth-modpack` | Downloads every server-side file of a `.mrpack` with sha512 checks, applies overrides, installs the loader. |
| `terraria`, `factorio`, `fivem`, `mindustry` | Game-specific official downloads. |

Downloads retry three times with back-off, resume interrupted transfers
with HTTP Range requests, stop immediately on 401/403/404, and delete a
file whose checksum does not match. Download URLs are validated against
private and loopback addresses.

`Dependencies` are Debian packages layered onto the runtime image once
per node (`docker run` + `docker commit`, tag `ndl-gs-runtime:<hash>`).
Only names in `KnownPackages` for the base image are accepted.

## Provisioning checks

`POST /api/v1/game-servers/preflight` returns exactly what create will do,
and create refuses when it reports errors:

- required variables, select options, numeric fields;
- install-stage requirements (for example a Steam account that owns the
  game) block creation; start-stage requirements (EULA, licence keys,
  tokens) are warnings until start; optional ones (GSLT) are notes;
- node architecture against the template's architectures;
- requested RAM against node memory, RAM already reserved by other game
  servers, the template minimum and recommendation; Java heap against RAM;
  CPUs against node threads; free disk;
- ports: the template's whole port set moves by one delta until every host
  port is free (reservations of other servers plus a bind probe). Ports
  with `Env` keep container == host and update their variable; `Fixed`
  ports keep their container port and are NAT-mapped; other ports are
  derived by offset and move with the set;
- placement: game servers run through the control node's container
  runtime, so other nodes are refused rather than silently ignored.

Secret variables never ship defaults. Variables with `Generate: "password"`
are filled with a random value when left empty.

## Lifecycle

Containers start with stdin open (`docker run -i`), so console commands
and graceful stop commands reach the server. Stop sends the template's
`Stop` command (`^C` means SIGINT), waits up to `StopTimeout` (30 s by
default) with `docker wait`, then falls back to `docker stop`. `HOME` is
the persistent server folder.

## Verification levels

Each catalogue item reports how far it has actually been verified:

| Level | Meaning |
|---|---|
| `schema` | Passes `ValidateTemplate` only. |
| `source` | App IDs, download sources, executables and ports were taken from, and cross-checked against, the reference in `SourceRef` (LinuxGSM configs, Pelican eggs, upstream docs or releases). |
| `installed` | A real install completed in an isolated Docker environment. |
| `started` | Installed, and the server process started and stayed up. |

`installed` and `started` are recorded by hand in `verification.go` from
runs of the live test (never from production hosts):

```text
NDL_LIVE_IDS=ndl-mindustry NDL_LIVE_OUT=/tmp/live \
  go test -tags livegs -run TestLiveCatalogue ./internal/gameserver/
```

## Adding a template

1. Find a reputable reference (LinuxGSM config, Pelican egg, official docs,
   upstream releases). Do not guess app IDs, URLs, arguments or ports.
2. Use the builder for the family and fill `GameTitle`, `Category`,
   `Engine`, `SourceRef`, `Requirements`, ports with `Env`/`Fixed`.
3. Run `go test ./internal/gameserver/`; `TestEveryBuiltinTemplateValidates`
   checks the schema, installers, images, placeholders, ports, resources
   and requirements.
4. Linux-native servers only: No-DAL ships no Wine or Proton runtime.
