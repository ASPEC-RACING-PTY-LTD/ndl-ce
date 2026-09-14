package gameserver

import "strings"

// HumanError turns a raw install/runtime failure into guidance while keeping
// the original text available to advanced users.
func HumanError(raw string) string {
	s := strings.ToLower(raw)
	switch {
	case strings.Contains(s, "license") && (strings.Contains(s, "fivem") || strings.Contains(s, "cfx") || strings.Contains(s, "sv_license")):
		return "FiveM needs a Cfx.re license key. Create one at portal.cfx.re and paste it into License key, then try Start again."
	case strings.Contains(s, "cluster_token") || strings.Contains(s, "cluster token") || strings.Contains(s, "klei"):
		return "Don't Starve Together needs a Klei cluster token. Create one at accounts.klei.com and paste it, then try Start again."
	case strings.Contains(s, "is required to start"):
		return "This server is installed, but a required key or token is still missing. Fill that field and Start again."
	case strings.Contains(s, "eula"):
		return "Minecraft will not start until you accept the EULA. Set Accept EULA to yes and save."
	case strings.Contains(s, "steam guard") || strings.Contains(s, "invalid password") && strings.Contains(s, "steam"):
		return "Steam rejected the login. This install uses anonymous SteamCMD. If the app requires an account, add those credentials in Advanced settings."
	case strings.Contains(s, "gslt") || strings.Contains(s, "steam token") || strings.Contains(s, "account token"):
		return "A Steam Game Server Login Token is missing. Create one at steamcommunity.com/dev/managegameservers and paste it into the token field."
	case strings.Contains(s, "no space") || strings.Contains(s, "no space left"):
		return "The node ran out of disk space while downloading the server. Free space or give this server a larger disk, then retry."
	case strings.Contains(s, "toomanyrequests") || strings.Contains(s, "rate limit"):
		return "The download source rate-limited this node. Wait a minute and retry the install."
	case strings.Contains(s, "unauthorized") || strings.Contains(s, "401"):
		return "The download was refused. Check tokens and that the image or file is public."
	case strings.Contains(s, "connection refused") || strings.Contains(s, "network is unreachable") || strings.Contains(s, "temporary failure in name resolution"):
		return "The node could not reach the internet to download the server. Check DNS and outbound access, then retry."
	case strings.Contains(s, "manifest unknown") || strings.Contains(s, "not found"):
		return "The container image or game build was not found. Check the version field and try latest."
	case strings.Contains(s, "docker") && strings.Contains(s, "permission"):
		return "Docker on this node refused the container. Game Servers do not use privileged mode. Check that the runtime user can talk to Docker."
	case strings.Contains(s, "cannot connect to the docker daemon") || strings.Contains(s, "docker daemon"):
		return "Docker is not running on the chosen node. Install Docker Engine on that node or pick a node that already has it."
	case strings.Contains(s, "address already in use") || strings.Contains(s, "bind"):
		return "The chosen port is already in use. Pick a different published port."
	case strings.Contains(s, "out of memory") || strings.Contains(s, "oom"):
		return "The server ran out of memory. Raise the RAM allocation or lower the Java heap / player limit."
	case strings.Contains(s, "exit status 1") || strings.Contains(s, "exit code 1"):
		return "The install script stopped with an error. Open the raw log below for the exact line."
	default:
		if strings.TrimSpace(raw) == "" {
			return "The operation failed. Open the raw log for details."
		}
		return "The operation failed. " + clip(strings.TrimSpace(raw), 180)
	}
}
