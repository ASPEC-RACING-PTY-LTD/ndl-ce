package debian

const WGUnsupportedHost = "WireGuard runtime uses the Debian 13 systemd-networkd adapter. This host is not Debian 13 amd64."

// WGRuntimePackages are optional. They are not Depends of ndl-agent.
var WGRuntimePackages = []string{"wireguard-tools"}
