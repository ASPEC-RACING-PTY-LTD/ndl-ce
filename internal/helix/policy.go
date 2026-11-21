package helix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
)

// Operation kinds Helix can ask for. Each names the capability it needs.
const (
	OpExec          = "exec"
	OpContainer     = "container"
	OpService       = "service"
	OpPower         = "power"
	OpDeploy        = "deploy"
	OpRollback      = "rollback"
	OpContainerLogs = "container.logs"
	OpServiceLogs   = "service.logs"
)

// Decision is what NDL-CE does with a request.
type Decision struct {
	// Allowed is false when the grant does not cover the request at all.
	Allowed bool
	// NeedsApproval holds the request for a SuperAdmin; Reasons says why.
	NeedsApproval bool
	Reasons       []string
}

// Request describes one change Helix asks for, for Decide.
type Request struct {
	Op string
	// Action is the verb for container, service and power operations.
	Action string
	// Command is the shell command for OpExec.
	Command string
}

// CapFor is the capability an operation needs.
func CapFor(op string) string {
	switch op {
	case OpExec:
		return CapExecute
	case OpContainer, OpService:
		return CapWrite
	case OpPower:
		return CapPower
	case OpDeploy, OpRollback:
		return CapDeploy
	case OpContainerLogs, OpServiceLogs:
		return CapLogs
	}
	return CapRead
}

// Decide applies a grant to a change request. Reads never come here.
func Decide(g Grant, req Request) Decision {
	if !g.Has(CapFor(req.Op)) {
		return Decision{}
	}
	d := Decision{Allowed: true}
	if req.Op == OpContainerLogs || req.Op == OpServiceLogs {
		return d
	}
	if g.Protected {
		d.Reasons = append(d.Reasons, "the machine is protected: every change needs approval")
	}
	destructive := DestructiveReasons(req)
	if len(destructive) > 0 && !g.Destructive {
		d.Reasons = append(d.Reasons, destructive...)
	}
	d.NeedsApproval = len(d.Reasons) > 0
	return d
}

// DestructiveReasons explains why a request is destructive, or returns nil.
func DestructiveReasons(req Request) []string {
	switch req.Op {
	case OpPower:
		if req.Action == "stop" || req.Action == "restart" {
			return []string{"stopping or restarting the machine interrupts everything on it"}
		}
	case OpContainer:
		if req.Action == "stop" {
			return []string{"stopping a container takes its service down"}
		}
	case OpService:
		if req.Action == "stop" || req.Action == "disable" {
			return []string{"stopping or disabling a system service takes it down"}
		}
	case OpRollback:
		return []string{"a rollback resets the project's folder to an older commit and discards local changes"}
	case OpExec:
		return CommandRisks(req.Command)
	}
	return nil
}

type risk struct {
	re     *regexp.Regexp
	reason string
}

// Commands that destroy data, cut access or run code NDL-CE cannot see.
// This catches mistakes, not a determined attacker: a shell can always
// hide what it does. Machines that must not be changed are Protected.
var commandRisks = []risk{
	{regexp.MustCompile(`(^|[\s;&|(])rm\s+(-[a-zA-Z]*\s+)*-[a-zA-Z]*[rR][a-zA-Z]*\s+(-[a-zA-Z]*\s+)*("?/"?|"?/\*|"?/(bin|boot|dev|etc|home|lib|lib64|opt|proc|root|run|sbin|srv|sys|usr|var)(/[^/\s]*)?/?"?)(\s|$|;)`), "recursive delete of a system folder"},
	{regexp.MustCompile(`(^|[\s;&|(])rm\s+.*--no-preserve-root`), "delete with --no-preserve-root"},
	{regexp.MustCompile(`(^|[\s;&|(])(mkfs(\.[a-z0-9]+)?|wipefs|shred|fdisk|sfdisk|gdisk|sgdisk|parted|blkdiscard)(\s|$)`), "disk formatting or wiping"},
	{regexp.MustCompile(`(^|[\s;&|(])dd\s+.*of=/dev/`), "writing to a raw device"},
	{regexp.MustCompile(`>\s*/dev/(sd|vd|nvme|xvd|hd|mmcblk)`), "writing to a raw device"},
	{regexp.MustCompile(`(^|[\s;&|(])(shutdown|reboot|poweroff|halt)(\s|$|;)|(^|[\s;&|(])init\s+[06](\s|$)|systemctl\s+(.*\s)?(poweroff|reboot|halt|kexec|emergency|rescue)(\s|$)`), "shutting down or rebooting"},
	{regexp.MustCompile(`(^|[\s;&|(])(userdel|deluser|groupdel|passwd|chpasswd|usermod)(\s|$)`), "changing accounts or passwords"},
	{regexp.MustCompile(`(^|[\s;&|(])(iptables|ip6tables)\s+(.*\s)?(-F|--flush|-X|-P\s+\S+\s+DROP)`), "changing the firewall"},
	{regexp.MustCompile(`(^|[\s;&|(])nft\s+(flush|delete)|(^|[\s;&|(])ufw\s+(disable|reset|deny)`), "changing the firewall"},
	{regexp.MustCompile(`docker\s+(system|volume|image|container|network|builder)\s+prune|docker\s+volume\s+rm|docker\s+(rm|rmi)\s|docker\s+compose\s+(.*\s)?down\s+(.*\s)?(-v|--volumes)|docker-compose\s+(.*\s)?down\s+(.*\s)?(-v|--volumes)`), "deleting Docker containers, images or volumes"},
	{regexp.MustCompile(`(^|[\s;&|(])(zfs|zpool)\s+destroy|(^|[\s;&|(])(lvremove|vgremove|pvremove)(\s|$)`), "deleting storage"},
	{regexp.MustCompile(`(?i)\b(drop\s+(database|schema|table)|truncate\s+table)\b`), "deleting a database or table"},
	{regexp.MustCompile(`git\s+push\s+(.*\s)?(-f|--force|--force-with-lease)(\s|$)|git\s+clean\s+-[a-zA-Z]*[fd]|git\s+reset\s+(.*\s)?--hard`), "discarding Git history or files"},
	{regexp.MustCompile(`(^|[\s;&|(])(chmod|chown|chgrp)\s+(-[a-zA-Z]*R[a-zA-Z]*\s+)(\S+\s+)?/(\s|$|etc|usr|var|bin|lib|root|home)`), "changing permissions across system folders"},
	{regexp.MustCompile(`(^|[\s;&|(])crontab\s+-r`), "deleting a crontab"},
	{regexp.MustCompile(`(^|[\s;&|(])(kill|pkill|killall)\s+(-9\s+|-KILL\s+|-s\s+KILL\s+)?(1|-1)(\s|$)`), "killing init or every process"},
	{regexp.MustCompile(`:\s*\(\s*\)\s*\{`), "fork bomb"},
	{regexp.MustCompile(`>\s*/etc/(passwd|shadow|group|sudoers|fstab|hosts)(\s|$)`), "overwriting a system file"},
	{regexp.MustCompile(`(base64\s+(-d|--decode)|xxd\s+-r)[^|]*\|\s*(ba|z|da)?sh\b|\beval\s`), "running hidden or generated code"},
	{regexp.MustCompile(`(curl|wget)\s[^|]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`), "running a script straight from the internet"},
	{regexp.MustCompile(`(^|[\s;&|(])(systemctl\s+(.*\s)?(stop|disable|mask)\s+(.*\s)?(ssh|sshd|networking|systemd-networkd|NetworkManager|docker|containerd)(\.service)?(\s|$))`), "stopping remote access, networking or Docker"},
}

// CommandRisks lists why a command is destructive, or nil.
func CommandRisks(cmd string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range commandRisks {
		if r.re.MatchString(cmd) && !seen[r.reason] {
			seen[r.reason] = true
			out = append(out, "the command looks destructive: "+r.reason)
		}
	}
	return out
}

// Fingerprint binds an approval to one exact request.
func Fingerprint(op, machineID string, params any) string {
	raw, _ := json.Marshal(params)
	sum := sha256.Sum256([]byte(op + "\x00" + machineID + "\x00" + string(raw)))
	return hex.EncodeToString(sum[:])
}

// Summary shortens text for audit entries and approval cards.
func Summary(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
