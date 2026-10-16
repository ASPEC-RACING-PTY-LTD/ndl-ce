package httpapi

import (
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestDockerPruneDueMelbourne(t *testing.T) {
	st := appdb.DockerPruneSettings{RunAt: "06:00", Timezone: "Australia/Melbourne"}
	loc, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		t.Skip("no tzdata")
	}
	before := time.Date(2026, 10, 17, 5, 59, 0, 0, loc)
	if got := dockerPruneDue(st, before); !got.Equal(time.Date(2026, 10, 16, 6, 0, 0, 0, loc)) {
		t.Fatalf("before 6am due=%v", got)
	}
	after := time.Date(2026, 10, 17, 6, 1, 0, 0, loc)
	if got := dockerPruneDue(st, after); !got.Equal(time.Date(2026, 10, 17, 6, 0, 0, 0, loc)) {
		t.Fatalf("after 6am due=%v", got)
	}
}
