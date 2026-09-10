package transport

// AgentSocket is the local Connect unix socket. Phase 1 binds it.
const AgentSocket = "/run/ndl/agent.sock"

// ControlSocket is the local root administration socket. systemd owns
// the path as mode 0600 so only UID 0 can connect. Remote HTTP is unchanged.
const ControlSocket = "/run/ndl/control.sock"
