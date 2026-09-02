package ai

import (
	"strings"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/metrics"
)

func TestCanQueryRequiresReads(t *testing.T) {
	if CanQuery(nil) || CanQuery([]string{"compute.read"}) {
		t.Fatal("profile without read must not query")
	}
	if !CanQuery(DefaultAskGrants()) {
		t.Fatal("ask grants")
	}
}

func TestRedactStripsKeys(t *testing.T) {
	in := `why restart api_key=sk-secretpassword Bearer abcdefg123`
	out := Redact("api_key=sk-abcdefghijklmnopqrstuvwxyz password=hunter2 Bearer abcdefg123 " + in)
	if strings.Contains(out, "sk-abcdefghijklmnopqrstuvwxyz") || strings.Contains(out, "hunter2") || strings.Contains(strings.ToLower(out), "bearer abc") {
		t.Fatalf("leaked %s", out)
	}
}

func TestLocalAnswerCitesRestart(t *testing.T) {
	ctx := BuildContext([]appdb.Event{{
		Type:      "workload.restarted",
		Payload:   []byte(`{"name":"jellyfin","reason":"guest qemu process exited","api_key":"sk-leakedkeyvalue"}`),
		CreatedAt: time.Now().UTC(),
	}}, []metrics.Series{{
		Name:   metrics.MetricCPUBusyRatio,
		Status: metrics.StatusAvailable,
		Points: []metrics.Point{{Time: time.Now().UTC(), Value: 0.91}},
	}}, 10)
	ans := LocalAnswer("Why did this workload restart?", ctx)
	if !strings.Contains(ans, "workload.restarted") || !strings.Contains(ans, "cpu.busy_ratio") {
		t.Fatalf("%s", ans)
	}
	if strings.Contains(ans, "sk-leakedkeyvalue") {
		t.Fatalf("secret in answer %s", ans)
	}
}

func TestNormalizeRejectsUnknown(t *testing.T) {
	if _, err := NormalizeKind("shell"); err == nil {
		t.Fatal("shell kind")
	}
	if _, err := NormalizeMode("exec"); err == nil {
		t.Fatal("exec mode")
	}
	if mode, err := NormalizeMode("operate"); err != nil || mode != ModeOperate {
		t.Fatalf("operate %v %v", mode, err)
	}
}
