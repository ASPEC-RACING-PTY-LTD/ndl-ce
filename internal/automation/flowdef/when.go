package flowdef

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/no-dal/ndl-ce/internal/automation/expr"
)

// Condition is one WHEN test on an event trigger. A list of conditions is
// read left to right; Join says how a condition joins the one before it.
// "and" binds tighter than "or", and Group nests a bracketed list.
type Condition struct {
	Join  string      `json:"join,omitempty"`
	Kind  string      `json:"kind,omitempty"`
	Field string      `json:"field,omitempty"`
	Op    string      `json:"op,omitempty"`
	Value any         `json:"value,omitempty"`
	Group []Condition `json:"group,omitempty"`
}

// ConditionOps are the payload tests a WHEN condition may use.
var ConditionOps = []string{"eq", "neq", "contains", "exists"}

// ConditionResult is how one condition evaluated, for test runs.
type ConditionResult struct {
	Path     string `json:"path"`
	Field    string `json:"field,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Op       string `json:"op,omitempty"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Matched  bool   `json:"matched"`
}

// ParseConditions reads the "conditions" of a trigger config.
func ParseConditions(v any) ([]Condition, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out []Condition
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("conditions must be a list")
	}
	return out, nil
}

// ValidateConditions checks joins, operators and nesting depth.
func ValidateConditions(conds []Condition, path string, depth int) []Problem {
	var out []Problem
	if depth > MaxConditionDepth {
		return []Problem{{Path: path, Message: fmt.Sprintf("conditions nest more than %d levels deep", MaxConditionDepth)}}
	}
	for i, c := range conds {
		p := fmt.Sprintf("%s[%d]", path, i)
		if c.Join != "" && c.Join != "and" && c.Join != "or" {
			out = append(out, Problem{Path: p + ".join", Message: "join must be and or or"})
		}
		if len(c.Group) > 0 {
			out = append(out, ValidateConditions(c.Group, p+".group", depth+1)...)
			continue
		}
		if c.Kind != "" && c.Field == "" {
			continue
		}
		if strings.TrimSpace(c.Field) == "" {
			out = append(out, Problem{Path: p + ".field", Message: "choose the event field to test"})
		}
		ok := false
		for _, op := range ConditionOps {
			if c.Op == op {
				ok = true
			}
		}
		if !ok {
			out = append(out, Problem{Path: p + ".op", Message: "op must be eq, neq, contains or exists"})
		}
	}
	return out
}

// MatchConditions evaluates conditions against an event: its kind and its
// payload. An empty list matches.
func MatchConditions(conds []Condition, kind string, payload map[string]any) (bool, []ConditionResult) {
	var results []ConditionResult
	ok := matchList(conds, kind, payload, "conditions", &results)
	return ok, results
}

func matchList(conds []Condition, kind string, payload map[string]any, path string, results *[]ConditionResult) bool {
	if len(conds) == 0 {
		return true
	}
	// Split into runs joined by or; each run is an and of its items.
	anyRun := false
	runOK := true
	for i, c := range conds {
		if i > 0 && c.Join == "or" {
			if runOK {
				anyRun = true
			}
			runOK = true
		}
		p := fmt.Sprintf("%s[%d]", path, i)
		var hit bool
		if len(c.Group) > 0 {
			hit = matchList(c.Group, kind, payload, p+".group", results)
		} else {
			hit = matchOne(c, kind, payload, p, results)
		}
		runOK = runOK && hit
	}
	return anyRun || runOK
}

func matchOne(c Condition, kind string, payload map[string]any, path string, results *[]ConditionResult) bool {
	if c.Kind != "" && c.Field == "" {
		hit := c.Kind == kind
		*results = append(*results, ConditionResult{Path: path, Kind: c.Kind, Actual: kind, Matched: hit})
		return hit
	}
	actual := expr.Lookup(payload, c.Field)
	var hit bool
	switch c.Op {
	case "eq":
		hit = expr.Equal(actual, c.Value)
	case "neq":
		hit = !expr.Equal(actual, c.Value)
	case "contains":
		v, _ := expr.Call("contains", actual, c.Value)
		hit, _ = v.(bool)
	case "exists":
		hit = actual != nil
	}
	*results = append(*results, ConditionResult{Path: path, Field: c.Field, Op: c.Op, Expected: c.Value, Actual: actual, Matched: hit})
	return hit
}
