package flowdef

import (
	"strings"
	"testing"
	"time"
)

type fakeCatalog map[string]NodeSpec

func (f fakeCatalog) NodeSpec(typ string) (NodeSpec, bool) {
	s, ok := f[typ]
	return s, ok
}

var testCatalog = fakeCatalog{
	"logic.condition":    {Branches: []string{"true", "false"}, Expressions: []string{"expression"}},
	"logic.foreach":      {Branches: []string{"each", "done"}, Loop: true, Required: []string{"items"}},
	"logic.set_variable": {SetsVariable: "name", Required: []string{"name"}},
	"action.notify":      {Required: []string{"message"}},
}

func manual(nodes []Node, edges []Edge) *Definition {
	return &Definition{SchemaVersion: 1, Trigger: Trigger{ID: TriggerID, Type: TriggerManual}, Nodes: nodes, Edges: edges}
}

func hasProblem(ps []Problem, path, text string) bool {
	for _, p := range ps {
		if strings.HasPrefix(p.Path, path) && strings.Contains(p.Message, text) {
			return true
		}
	}
	return false
}

func TestValidateGoodFlow(t *testing.T) {
	d := manual(
		[]Node{
			{ID: "c", Type: "logic.condition", Config: map[string]any{"expression": "trigger.x == 1"}},
			{ID: "n", Type: "action.notify", Config: map[string]any{"message": "x is {{ nodes.c.output.result }}"}},
		},
		[]Edge{{From: TriggerID, To: "c"}, {From: "c", To: "n", Port: "true"}},
	)
	if ps := Validate(d, testCatalog); len(ps) != 0 {
		t.Fatalf("%+v", ps)
	}
}

func TestValidateFindsCyclesAndUnreachable(t *testing.T) {
	d := manual(
		[]Node{
			{ID: "a", Type: "action.notify", Config: map[string]any{"message": "a"}},
			{ID: "b", Type: "action.notify", Config: map[string]any{"message": "b"}},
			{ID: "lonely", Type: "action.notify", Config: map[string]any{"message": "c"}},
		},
		[]Edge{{From: TriggerID, To: "a"}, {From: "a", To: "b"}, {From: "b", To: "a"}},
	)
	ps := Validate(d, testCatalog)
	if !hasProblem(ps, "edges", "a → b → a") {
		t.Fatalf("cycle: %+v", ps)
	}
	d.Edges = []Edge{{From: TriggerID, To: "a"}, {From: "a", To: "b"}}
	ps = Validate(d, testCatalog)
	if !hasProblem(ps, "nodes[2]", "never reached") || len(ps) != 1 {
		t.Fatalf("unreachable: %+v", ps)
	}
}

func TestValidateFieldPaths(t *testing.T) {
	d := manual(
		[]Node{
			{ID: "c", Type: "logic.condition", Config: map[string]any{"expression": "trigger.x =="}},
			{ID: "n", Type: "action.notify", Config: map[string]any{"message": "{{ nodes.ghost.output }}"}},
			{ID: "n", Type: "action.notify"},
			{ID: "x", Type: "action.nope"},
		},
		[]Edge{{From: TriggerID, To: "c"}, {From: "c", To: "n"}, {From: "c", To: "missing", Port: "true"}},
	)
	ps := Validate(d, testCatalog)
	for _, want := range [][2]string{
		{"nodes[0].config.expression", "ends early"},
		{"nodes[1].config.message", "ghost"},
		{"nodes[2].id", "used twice"},
		{"nodes[3].type", "unknown step type"},
		{"edges[1].port", "choose which branch"},
		{"edges[2].to", "unknown step"},
	} {
		if !hasProblem(ps, want[0], want[1]) {
			t.Fatalf("missing %v in %+v", want, ps)
		}
	}
}

func TestValidateLoopBodyAndParallelVariables(t *testing.T) {
	d := manual(
		[]Node{
			{ID: "loop", Type: "logic.foreach", Config: map[string]any{"items": "{{ trigger.list }}"}},
			{ID: "inner", Type: "action.notify", Config: map[string]any{"message": "{{ item }}"}},
			{ID: "after", Type: "action.notify", Config: map[string]any{"message": "done"}},
		},
		[]Edge{{From: TriggerID, To: "loop"}, {From: "loop", To: "inner", Port: "each"}, {From: "inner", To: "after"}, {From: "loop", To: "after", Port: "done"}},
	)
	if ps := Validate(d, testCatalog); !hasProblem(ps, "edges", "outside the loop") {
		t.Fatalf("%+v", ps)
	}
	d.Edges = d.Edges[:2]
	d.Edges = append(d.Edges, Edge{From: "loop", To: "after", Port: "done"})
	if ps := Validate(d, testCatalog); len(ps) != 0 {
		t.Fatalf("%+v", ps)
	}
	v := manual(
		[]Node{
			{ID: "a", Type: "logic.set_variable", Config: map[string]any{"name": "x"}},
			{ID: "b", Type: "logic.set_variable", Config: map[string]any{"name": "x"}},
		},
		[]Edge{{From: TriggerID, To: "a"}, {From: TriggerID, To: "b"}},
	)
	if ps := Validate(v, testCatalog); !hasProblem(ps, "nodes", "both set x") {
		t.Fatalf("%+v", ps)
	}
	// Exclusive branches may write the same variable.
	v.Nodes = append(v.Nodes, Node{ID: "c", Type: "logic.condition", Config: map[string]any{"expression": "true"}})
	v.Edges = []Edge{{From: TriggerID, To: "c"}, {From: "c", To: "a", Port: "true"}, {From: "c", To: "b", Port: "false"}}
	if ps := Validate(v, testCatalog); len(ps) != 0 {
		t.Fatalf("%+v", ps)
	}
}

func TestValidateTriggers(t *testing.T) {
	d := manual(nil, nil)
	d.Trigger = Trigger{ID: TriggerID, Type: TriggerSchedule, Config: map[string]any{"preset": "cron", "cron": "61 * * * *"}}
	if ps := Validate(d, testCatalog); !hasProblem(ps, "trigger.config.cron", "minute") {
		t.Fatalf("%+v", ps)
	}
	d.Trigger.Config = map[string]any{"preset": "daily", "timezone": "Mars/Base"}
	if ps := Validate(d, testCatalog); !hasProblem(ps, "trigger.config.timezone", "unknown timezone") {
		t.Fatalf("%+v", ps)
	}
	deep := []any{map[string]any{"group": []any{map[string]any{"group": []any{map[string]any{"group": []any{map[string]any{"field": "a", "op": "eq"}}}}}}}}
	d.Trigger = Trigger{ID: TriggerID, Type: TriggerEvent, Config: map[string]any{"kind": "backup.failed", "conditions": deep}}
	if ps := Validate(d, testCatalog); !hasProblem(ps, "trigger.config.conditions", "levels deep") {
		t.Fatalf("%+v", ps)
	}
}

func TestWhenConditionsAndBindsTighterThanOr(t *testing.T) {
	payload := map[string]any{"pool": map[string]any{"name": "fast"}, "percent": "91", "tags": []any{"prod"}}
	// a and b or c: (false and true) or true = true
	conds := []Condition{
		{Field: "pool.name", Op: "eq", Value: "slow"},
		{Join: "and", Field: "percent", Op: "exists"},
		{Join: "or", Field: "tags", Op: "contains", Value: "prod"},
	}
	ok, res := MatchConditions(conds, "storage.pool_usage_high", payload)
	if !ok || len(res) != 3 || res[0].Matched || !res[2].Matched {
		t.Fatalf("%v %+v", ok, res)
	}
	// a and (b or c) with a false is false.
	conds = []Condition{
		{Field: "pool.name", Op: "eq", Value: "slow"},
		{Join: "and", Group: []Condition{{Field: "percent", Op: "eq", Value: 91}, {Join: "or", Field: "x", Op: "exists"}}},
	}
	if ok, _ := MatchConditions(conds, "", payload); ok {
		t.Fatal("grouped and")
	}
	conds[0] = Condition{Field: "pool.name", Op: "neq", Value: "slow"}
	if ok, _ := MatchConditions(conds, "", payload); !ok {
		t.Fatal("number equals text")
	}
	if ok, _ := MatchConditions([]Condition{{Kind: "backup.failed"}}, "backup.succeeded", payload); ok {
		t.Fatal("kind")
	}
	if ok, _ := MatchConditions(nil, "x", nil); !ok {
		t.Fatal("empty matches")
	}
}

func TestCronNext(t *testing.T) {
	base := time.Date(2026, 10, 20, 10, 7, 30, 0, time.UTC)
	cases := map[string]time.Time{
		"*/5 * * * *":      time.Date(2026, 10, 20, 10, 10, 0, 0, time.UTC),
		"0 * * * *":        time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC),
		"30 2 * * *":       time.Date(2026, 10, 21, 2, 30, 0, 0, time.UTC),
		"0 9 * * mon":      time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC),
		"0 0 1 jan *":      time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		"15 10 * * 1-5":    time.Date(2026, 10, 20, 10, 15, 0, 0, time.UTC),
		"0 12 13 * 5":      time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), // dom or dow
		"0,30 8-9/1 * * *": time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC),
	}
	for line, want := range cases {
		c, err := ParseCron(line)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if got := c.Next(base); !got.Equal(want) {
			t.Fatalf("%s: %v want %v", line, got, want)
		}
	}
	for _, bad := range []string{"* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a * * * *"} {
		if _, err := ParseCron(bad); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
	spec := ScheduleSpec{Preset: "daily", At: "03:30", Timezone: "Australia/Sydney"}
	next, err := spec.Next(base)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Australia/Sydney")
	if l := next.In(loc); l.Hour() != 3 || l.Minute() != 30 {
		t.Fatalf("%v", l)
	}
}

func TestLimitsClampToHardCaps(t *testing.T) {
	l := Limits{MaxLoopItems: 5000, RunTimeoutSec: 999999, Concurrency: -3}.Normalized()
	if l.MaxLoopItems != 1000 || l.RunTimeoutSec != 86400 || l.Concurrency != 1 || l.MaxSteps != 500 || l.PerMinute != 6 || l.PerHour != 60 {
		t.Fatalf("%+v", l)
	}
}
