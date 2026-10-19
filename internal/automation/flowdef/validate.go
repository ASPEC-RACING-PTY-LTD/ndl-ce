package flowdef

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/no-dal/ndl-ce/internal/automation/expr"
)

// Problem is one validation error. Path points at the field in the
// definition so the editor can highlight it, such as "nodes[2].config.url".
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// NodeSpec is what validation needs to know about a node type.
type NodeSpec struct {
	Branches []string
	Loop     bool
	// SetsVariable names the config key holding the variable a node writes.
	SetsVariable string
	// Required config keys and the expression-valued keys.
	Required    []string
	Expressions []string
}

// Catalog looks up node types.
type Catalog interface {
	NodeSpec(typ string) (NodeSpec, bool)
}

// EventKinds are the events an event trigger may listen for.
var EventKinds = []string{
	"workload.started", "workload.stopped", "workload.restarted", "workload.deleted",
	"uptime.monitor_down", "uptime.monitor_recovered",
	"docker.container_unhealthy",
	"backup.succeeded", "backup.failed",
	"storage.pool_usage_high",
}

var nodeIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,62}$`)

// Validate checks a definition. An empty result means it can be published.
func Validate(d *Definition, cat Catalog) []Problem {
	var out []Problem
	add := func(path, format string, args ...any) {
		out = append(out, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if d.SchemaVersion != SchemaVersion {
		add("schema_version", "schema_version must be %d", SchemaVersion)
	}
	out = append(out, validateTrigger(d)...)
	if len(d.Nodes) > MaxNodes {
		add("nodes", "a flow has at most %d steps", MaxNodes)
	}
	ids := map[string]int{}
	for i, n := range d.Nodes {
		p := fmt.Sprintf("nodes[%d]", i)
		if !nodeIDPattern.MatchString(n.ID) {
			add(p+".id", "step id must start with a letter and use letters, digits, - or _")
		} else if n.ID == TriggerID {
			add(p+".id", "%q is reserved for the trigger", TriggerID)
		} else if _, dup := ids[n.ID]; dup {
			add(p+".id", "step id %q is used twice", n.ID)
		} else {
			ids[n.ID] = i
		}
		if strings.TrimSpace(n.Type) == "" {
			add(p+".type", "choose what this step does")
			continue
		}
		if strings.HasPrefix(n.Type, "trigger.") {
			add(p+".type", "a flow has one trigger; %s cannot be a step", n.Type)
			continue
		}
		spec, ok := cat.NodeSpec(n.Type)
		if !ok {
			add(p+".type", "unknown step type %s", n.Type)
			continue
		}
		for _, key := range spec.Required {
			if isBlank(n.Config[key]) {
				add(p+".config."+key, "%s is required", key)
			}
		}
		for _, key := range spec.Expressions {
			s, _ := n.Config[key].(string)
			if strings.TrimSpace(s) == "" {
				continue
			}
			if _, err := expr.Parse(stripBraces(s)); err != nil {
				add(p+".config."+key, "%v", err)
			}
		}
		if n.OnError != "" && n.OnError != OnErrorStop && n.OnError != OnErrorContinue && n.OnError != OnErrorFailBranch {
			add(p+".on_error", "on_error must be stop, continue or fail_branch")
		}
		if n.Retry != nil {
			out = append(out, validateRetry(*n.Retry, p+".retry")...)
		}
		if n.TimeoutMS < 0 {
			add(p+".timeout_ms", "timeout cannot be negative")
		}
		out = append(out, validateTemplates(n.Config, p+".config", ids, d)...)
	}
	if d.Retry != nil {
		out = append(out, validateRetry(*d.Retry, "retry")...)
	}
	if d.Error != nil && d.Error.OnError != "" && d.Error.OnError != OnErrorStop && d.Error.OnError != OnErrorContinue && d.Error.OnError != OnErrorFailBranch {
		add("error.on_error", "on_error must be stop, continue or fail_branch")
	}
	known := func(id string) bool {
		_, ok := ids[id]
		return ok
	}
	specOf := func(id string) NodeSpec {
		if n := d.Node(id); n != nil {
			s, _ := cat.NodeSpec(n.Type)
			return s
		}
		return NodeSpec{}
	}
	for i, e := range d.Edges {
		p := fmt.Sprintf("edges[%d]", i)
		if e.From == "" || e.To == "" {
			add(p, "an edge needs both ends")
			continue
		}
		if e.From != TriggerID && !known(e.From) {
			add(p+".from", "edge starts at unknown step %q", e.From)
		}
		if !known(e.To) {
			add(p+".to", "edge ends at unknown step %q", e.To)
		}
		if e.From == e.To {
			add(p, "a step cannot lead to itself")
		}
		if e.From == TriggerID {
			if e.Port != "" {
				add(p+".port", "the trigger has no branches")
			}
			continue
		}
		if !known(e.From) || e.Port == "" || e.Port == ErrorPort {
			if e.Port == "" && len(specOf(e.From).Branches) > 0 {
				add(p+".port", "choose which branch of %s this edge belongs to", e.From)
			}
			continue
		}
		branches := specOf(e.From).Branches
		ok := false
		for _, b := range branches {
			if b == e.Port {
				ok = true
			}
		}
		if !ok && !switchCase(d.Node(e.From), e.Port) {
			add(p+".port", "%s has no branch %q", e.From, e.Port)
		}
	}
	if len(out) > 0 {
		return out
	}
	if cyc := findCycle(d); len(cyc) > 0 {
		add("edges", "the steps loop back on themselves: %s", strings.Join(cyc, " → "))
		return out
	}
	reach := reachable(d, TriggerID)
	for i, n := range d.Nodes {
		if !reach[n.ID] {
			add(fmt.Sprintf("nodes[%d]", i), "step %s is never reached from the trigger", n.ID)
		}
	}
	out = append(out, validateLoops(d, cat)...)
	out = append(out, validateParallelVars(d, cat)...)
	return out
}

// switchCase lets a switch node's edges name its configured cases.
func switchCase(n *Node, port string) bool {
	if n == nil || n.Type != "logic.switch" {
		return false
	}
	cases, _ := n.Config["cases"].([]any)
	for _, c := range cases {
		switch x := c.(type) {
		case string:
			if x == port {
				return true
			}
		case map[string]any:
			if fmt.Sprint(x["port"]) == port || fmt.Sprint(x["value"]) == port {
				return true
			}
		}
	}
	return false
}

func isBlank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	}
	return false
}

// stripBraces lets expression fields be written with or without {{ }}.
func stripBraces(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") && strings.Count(t, "{{") == 1 {
		return strings.TrimSpace(t[2 : len(t)-2])
	}
	return t
}

// StripBraces is stripBraces for the engine.
func StripBraces(s string) string { return stripBraces(s) }

func validateRetry(r Retry, path string) []Problem {
	var out []Problem
	switch r.Mode {
	case "", "none", "fixed", "exponential":
	default:
		out = append(out, Problem{Path: path + ".mode", Message: "retry mode must be none, fixed or exponential"})
	}
	if r.MaxAttempts < 0 || r.MaxAttempts > MaxRetries {
		out = append(out, Problem{Path: path + ".max_attempts", Message: fmt.Sprintf("attempts must be 1 to %d", MaxRetries)})
	}
	if r.DelayMS < 0 || r.MaxDelayMS < 0 {
		out = append(out, Problem{Path: path + ".delay_ms", Message: "delays cannot be negative"})
	}
	return out
}

func validateTrigger(d *Definition) []Problem {
	var out []Problem
	t := d.Trigger
	if t.Type == "" {
		return []Problem{{Path: "trigger.type", Message: "choose how the flow starts"}}
	}
	if !strings.HasPrefix(t.Type, "trigger.") {
		return []Problem{{Path: "trigger.type", Message: "the trigger type must start with trigger."}}
	}
	if t.ID != TriggerID {
		out = append(out, Problem{Path: "trigger.id", Message: "the trigger id is " + TriggerID})
	}
	switch t.Type {
	case TriggerManual, TriggerWebhook:
	case TriggerSchedule:
		spec := ScheduleFromConfig(t.Config)
		line, err := spec.CronLine()
		if err != nil {
			field := "preset"
			if spec.Preset == "cron" {
				field = "cron"
			}
			out = append(out, Problem{Path: "trigger.config." + field, Message: err.Error()})
			break
		}
		if _, err := ParseCron(line); err != nil {
			out = append(out, Problem{Path: "trigger.config.cron", Message: err.Error()})
		}
		if _, err := spec.Location(); err != nil {
			out = append(out, Problem{Path: "trigger.config.timezone", Message: err.Error()})
		}
	case TriggerEvent:
		kind, _ := t.Config["kind"].(string)
		ok := false
		for _, k := range EventKinds {
			if k == kind {
				ok = true
			}
		}
		if !ok {
			out = append(out, Problem{Path: "trigger.config.kind", Message: "choose an event"})
		}
		conds, err := ParseConditions(t.Config["conditions"])
		if err != nil {
			out = append(out, Problem{Path: "trigger.config.conditions", Message: err.Error()})
		} else {
			out = append(out, ValidateConditions(conds, "trigger.config.conditions", 1)...)
		}
	default:
		out = append(out, Problem{Path: "trigger.type", Message: "unknown trigger " + t.Type})
	}
	return out
}

// validateTemplates parses every {{ }} in a node's config and checks that
// nodes.<id> references name steps that exist.
func validateTemplates(v any, path string, ids map[string]int, d *Definition) []Problem {
	var out []Problem
	switch x := v.(type) {
	case string:
		if !expr.HasTemplate(x) && !strings.Contains(x, "{{") {
			return nil
		}
		exprs, err := expr.CheckTemplate(x)
		if err != nil {
			return []Problem{{Path: path, Message: err.Error()}}
		}
		for _, e := range exprs {
			for _, ref := range e.Paths() {
				parts := strings.Split(ref, ".")
				if parts[0] == "nodes" && len(parts) > 1 && d.Node(parts[1]) == nil {
					out = append(out, Problem{Path: path, Message: fmt.Sprintf("%s refers to step %s, which does not exist", ref, parts[1])})
				}
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, validateTemplates(x[k], path+"."+k, ids, d)...)
		}
	case []any:
		for i, item := range x {
			out = append(out, validateTemplates(item, fmt.Sprintf("%s[%d]", path, i), ids, d)...)
		}
	}
	return out
}

func findCycle(d *Definition) []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var found []string
	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = grey
		stack = append(stack, id)
		for _, e := range d.Outgoing(id) {
			switch color[e.To] {
			case grey:
				for i, s := range stack {
					if s == e.To {
						found = append(append([]string{}, stack[i:]...), e.To)
						return true
					}
				}
			case white:
				if visit(e.To) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}
	starts := []string{TriggerID}
	for _, n := range d.Nodes {
		starts = append(starts, n.ID)
	}
	for _, s := range starts {
		if color[s] == white && visit(s) {
			return found
		}
	}
	return nil
}

func reachable(d *Definition, from string) map[string]bool {
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range d.Outgoing(id) {
			if !seen[e.To] {
				seen[e.To] = true
				stack = append(stack, e.To)
			}
		}
	}
	return seen
}

// validateLoops keeps a foreach body closed. The body is everything reached
// from the loop's each branch, so it can never lead outside; a body step
// must also not be entered from outside, such as from the done branch.
func validateLoops(d *Definition, cat Catalog) []Problem {
	var out []Problem
	isLoop := func(typ string) bool {
		s, _ := cat.NodeSpec(typ)
		return s.Loop
	}
	bodies := d.LoopBodies(isLoop)
	loops := make([]string, 0, len(bodies))
	for l := range bodies {
		loops = append(loops, l)
	}
	sort.Strings(loops)
	for _, loop := range loops {
		body := bodies[loop]
		for _, e := range d.Edges {
			if body[e.To] && !body[e.From] && !(e.From == loop && e.Port == "each") {
				out = append(out, Problem{Path: "edges", Message: fmt.Sprintf("step %s repeats inside %s but is also reached from %s outside the loop", e.To, loop, e.From)})
			}
		}
	}
	return out
}

// validateParallelVars refuses two set_variable steps that can run at the
// same time and write the same variable.
func validateParallelVars(d *Definition, cat Catalog) []Problem {
	type writer struct{ id, name string }
	var writers []writer
	for _, n := range d.Nodes {
		s, _ := cat.NodeSpec(n.Type)
		if s.SetsVariable == "" {
			continue
		}
		name, _ := n.Config[s.SetsVariable].(string)
		if name != "" {
			writers = append(writers, writer{n.ID, name})
		}
	}
	if len(writers) < 2 {
		return nil
	}
	reach := map[string]map[string]bool{}
	for _, w := range writers {
		reach[w.id] = reachable(d, w.id)
	}
	// under[x|port] is everything reached through one branch of x.
	under := map[string]map[string]bool{}
	for _, n := range d.Nodes {
		for _, e := range d.Outgoing(n.ID) {
			if e.Port == "" {
				continue
			}
			key := n.ID + "|" + e.Port
			if under[key] == nil {
				under[key] = map[string]bool{}
			}
			under[key][e.To] = true
			for id := range reachable(d, e.To) {
				under[key][id] = true
			}
		}
	}
	exclusive := func(a, b string) bool {
		for ka, sa := range under {
			for kb, sb := range under {
				na, pa, _ := strings.Cut(ka, "|")
				nb, pb, _ := strings.Cut(kb, "|")
				if na == nb && pa != pb && sa[a] && sb[b] && !sa[b] && !sb[a] {
					return true
				}
			}
		}
		return false
	}
	var out []Problem
	for i := 0; i < len(writers); i++ {
		for j := i + 1; j < len(writers); j++ {
			a, b := writers[i], writers[j]
			if a.name != b.name || reach[a.id][b.id] || reach[b.id][a.id] || exclusive(a.id, b.id) {
				continue
			}
			out = append(out, Problem{Path: "nodes", Message: fmt.Sprintf("steps %s and %s run in parallel and both set %s", a.id, b.id, a.name)})
		}
	}
	return out
}
