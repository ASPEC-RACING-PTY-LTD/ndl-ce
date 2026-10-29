package ndnet

import "testing"

func TestCloudflareAgentAddresses(t *testing.T) {
	if h, ok := CloudflareAgentHost("Cloudflare:Agent.Example.com"); !ok || h != "agent.example.com" {
		t.Fatalf("host %q %v", h, ok)
	}
	for _, good := range []string{"cloudflare:agent.example.com", "203.0.113.5:9444", "127.0.0.1:19444"} {
		if err := ValidListenAddr(good); err != nil {
			t.Fatalf("%s: %v", good, err)
		}
	}
	for _, bad := range []string{"cloudflare:", "cloudflare:not a host", "cloudflare:nodot", "cloudflare:a/b.com"} {
		if err := ValidListenAddr(bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}
