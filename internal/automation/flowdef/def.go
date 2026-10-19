// Package flowdef is the automation flow definition: its JSON model,
// validation, WHEN conditions, schedules and limits. It has no runtime
// dependencies; the engine and the API both build on it.
package flowdef

import (
	"encoding/json"
	"fmt"
)

// SchemaVersion is the only definition version this build reads.
const SchemaVersion = 1

// TriggerID is the fixed id of a flow's trigger in edges and expressions.
const TriggerID = "trigger"

// Trigger types.
const (
	TriggerManual   = "trigger.manual"
	TriggerSchedule = "trigger.schedule"
	TriggerWebhook  = "trigger.webhook"
	TriggerEvent    = "trigger.event"
)

// TriggerKinds maps a trigger type to the short kind stored on runs.
var TriggerKinds = map[string]string{
	TriggerManual:   "manual",
	TriggerSchedule: "schedule",
	TriggerWebhook:  "webhook",
	TriggerEvent:    "event",
}

// On-error modes.
const (
	OnErrorStop       = "stop"
	OnErrorContinue   = "continue"
	OnErrorFailBranch = "fail_branch"
)

// ErrorPort is the edge port taken by a node whose on_error is fail_branch.
const ErrorPort = "error"

// Definition is one flow: a trigger, nodes and the edges between them.
type Definition struct {
	SchemaVersion int            `json:"schema_version"`
	Trigger       Trigger        `json:"trigger"`
	Nodes         []Node         `json:"nodes"`
	Edges         []Edge         `json:"edges"`
	Variables     map[string]any `json:"variables,omitempty"`
	Retry         *Retry         `json:"retry,omitempty"`
	Error         *ErrorPolicy   `json:"error,omitempty"`
}

// Trigger starts a flow. Exactly one per flow.
type Trigger struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Config   map[string]any `json:"config,omitempty"`
	Position *Position      `json:"position,omitempty"`
}

// Node is one step. Position is only for the editor canvas.
type Node struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Name      string         `json:"name,omitempty"`
	Config    map[string]any `json:"config,omitempty"`
	Position  *Position      `json:"position,omitempty"`
	Retry     *Retry         `json:"retry,omitempty"`
	OnError   string         `json:"on_error,omitempty"`
	TimeoutMS int            `json:"timeout_ms,omitempty"`
}

// Position is a canvas coordinate.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Edge joins two nodes. Port names the branch of the source node it
// belongs to ("true", "false", a switch case, "each", "done", "error").
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Port string `json:"port,omitempty"`
}

// Retry is how a failing idempotent node is tried again.
type Retry struct {
	Mode        string `json:"mode"`
	MaxAttempts int    `json:"max_attempts"`
	DelayMS     int    `json:"delay_ms,omitempty"`
	MaxDelayMS  int    `json:"max_delay_ms,omitempty"`
}

// ErrorPolicy is the flow-wide default for failing nodes.
type ErrorPolicy struct {
	OnError string `json:"on_error"`
}

// Parse reads a definition. Unknown fields are kept out by the struct.
func Parse(raw []byte) (*Definition, error) {
	var d Definition
	if len(raw) == 0 {
		return nil, fmt.Errorf("definition is empty")
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("definition is not valid JSON: %v", err)
	}
	if d.Trigger.ID == "" {
		d.Trigger.ID = TriggerID
	}
	return &d, nil
}

// Encode writes a definition as JSON.
func (d *Definition) Encode() json.RawMessage {
	raw, _ := json.Marshal(d)
	return raw
}

// Node finds a node by id.
func (d *Definition) Node(id string) *Node {
	for i := range d.Nodes {
		if d.Nodes[i].ID == id {
			return &d.Nodes[i]
		}
	}
	return nil
}

// TriggerKind is the short kind of the trigger.
func (d *Definition) TriggerKind() string {
	return TriggerKinds[d.Trigger.Type]
}

// EventKind is the event an event trigger listens for.
func (d *Definition) EventKind() string {
	if d.Trigger.Type != TriggerEvent {
		return ""
	}
	s, _ := d.Trigger.Config["kind"].(string)
	return s
}

// OnErrorFor resolves a node's on_error against the flow default.
func (d *Definition) OnErrorFor(n *Node) string {
	if n != nil && n.OnError != "" {
		return n.OnError
	}
	if d.Error != nil && d.Error.OnError != "" {
		return d.Error.OnError
	}
	return OnErrorStop
}

// RetryFor resolves a node's retry policy against the flow default.
func (d *Definition) RetryFor(n *Node) Retry {
	r := Retry{Mode: "none", MaxAttempts: 1}
	if d.Retry != nil {
		r = *d.Retry
	}
	if n != nil && n.Retry != nil {
		r = *n.Retry
	}
	if r.MaxAttempts < 1 {
		r.MaxAttempts = 1
	}
	if r.MaxAttempts > MaxRetries {
		r.MaxAttempts = MaxRetries
	}
	if r.Mode == "" {
		r.Mode = "none"
	}
	return r
}

// Outgoing lists the edges leaving id.
func (d *Definition) Outgoing(id string) []Edge {
	var out []Edge
	for _, e := range d.Edges {
		if e.From == id {
			out = append(out, e)
		}
	}
	return out
}

// Incoming lists the edges entering id.
func (d *Definition) Incoming(id string) []Edge {
	var out []Edge
	for _, e := range d.Edges {
		if e.To == id {
			out = append(out, e)
		}
	}
	return out
}

// LoopBodies maps each foreach node to the set of nodes it repeats: every
// node reachable from its "each" port.
func (d *Definition) LoopBodies(isLoop func(typ string) bool) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, n := range d.Nodes {
		if !isLoop(n.Type) {
			continue
		}
		body := map[string]bool{}
		var stack []string
		for _, e := range d.Outgoing(n.ID) {
			if e.Port == "each" {
				stack = append(stack, e.To)
			}
		}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if body[id] || id == n.ID {
				continue
			}
			body[id] = true
			for _, e := range d.Outgoing(id) {
				stack = append(stack, e.To)
			}
		}
		out[n.ID] = body
	}
	return out
}

// Owners maps each node to the innermost foreach that repeats it ("" for
// the top level).
func Owners(d *Definition, bodies map[string]map[string]bool) map[string]string {
	out := map[string]string{}
	for _, n := range d.Nodes {
		best, bestSize := "", -1
		for loop, body := range bodies {
			if body[n.ID] && (bestSize < 0 || len(body) < bestSize) {
				best, bestSize = loop, len(body)
			}
		}
		out[n.ID] = best
	}
	return out
}
