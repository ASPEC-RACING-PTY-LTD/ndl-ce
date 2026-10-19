package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/automation/expr"
	"github.com/no-dal/ndl-ce/internal/automation/flowdef"
)

// BuiltinDeps are what the built-in HTTP steps need from the app.
type BuiltinDeps struct {
	// HTTPClient returns a client; when allowPrivate is false it must refuse
	// private, loopback and metadata addresses when it dials.
	HTTPClient func(allowPrivate bool, timeout time.Duration) *http.Client
	// CheckURL refuses a destination before any request is sent.
	CheckURL func(raw string) error
	// CheckSelf refuses a destination that reaches the platform's own API
	// unless the running flow may call it. It applies even when private
	// network addresses are allowed.
	CheckSelf func(ctx context.Context, in *ExecInput, raw string) error
}

// MaxResponseBytes caps what an HTTP step reads from a response.
const MaxResponseBytes = 1 << 20

var ruleOps = []Option{
	{"==", "is equal to"}, {"!=", "is not equal to"}, {">", "is greater than"}, {">=", "is at least"},
	{"<", "is less than"}, {"<=", "is at most"}, {"contains", "contains"}, {"not_contains", "does not contain"},
	{"starts_with", "starts with"}, {"is_empty", "is empty"}, {"not_empty", "is not empty"},
}

func registerBuiltins(r *Registry, deps BuiltinDeps) {
	r.Register(NodeType{
		Type: "logic.condition", DisplayName: "Condition", Category: CategoryLogic, Idempotent: true,
		Description: "Takes the Yes branch when the rules match, otherwise the No branch.",
		Branches:    []string{"true", "false"},
		Expressions: []string{"expression"},
		Fields: []Field{
			{Key: "match", Label: "Match", Type: "select", Default: "all", Options: []Option{{"all", "All rules"}, {"any", "Any rule"}}},
			{Key: "rules", Label: "Rules", Type: "rules", Help: "Each rule compares two values. Insert dynamic content with {{ }}."},
			{Key: "expression", Label: "Expression", Type: "expression", Help: "Used when there are no rules, for example trigger.percent >= 85 and not isNull(vars.owner)."},
		},
		Execute: execCondition,
	})
	r.Register(NodeType{
		Type: "logic.switch", DisplayName: "Switch", Category: CategoryLogic, Idempotent: true,
		Description: "Takes the branch whose case equals the value, or Default.",
		Branches:    []string{"default"},
		Fields: []Field{
			{Key: "value", Label: "Value", Type: "text", Required: true, Placeholder: "{{ trigger.status }}"},
			{Key: "cases", Label: "Cases", Type: "cases", Required: true, Help: "One branch per case."},
		},
		Execute: execSwitch,
	})
	r.Register(NodeType{
		Type: "logic.foreach", DisplayName: "For each", Category: CategoryLogic, Idempotent: true, Loop: true,
		Description: "Repeats the steps inside it once per item. Inside, {{ item }} is the current item and {{ index }} its position.",
		Branches:    []string{"each", "done"},
		Fields: []Field{
			{Key: "items", Label: "Items", Type: "text", Required: true, Placeholder: "{{ nodes.list.output.items }}"},
			{Key: "max", Label: "At most", Type: "number", Help: "Stops after this many items. The flow's loop limit still applies."},
		},
	})
	r.Register(NodeType{
		Type: "logic.delay", DisplayName: "Delay", Category: CategoryLogic, Idempotent: true,
		Description: "Waits, then continues. The run is parked meanwhile and survives a restart.",
		Fields: []Field{
			{Key: "duration", Label: "Wait for", Type: "duration", Placeholder: "10m", Help: "Such as 30s, 10m or 2h."},
			{Key: "until", Label: "Or wait until", Type: "text", Placeholder: "2026-01-01T09:00:00Z"},
		},
		Execute: execDelay,
	})
	r.Register(NodeType{
		Type: "logic.set_variable", DisplayName: "Set variable", Category: CategoryLogic, Idempotent: true, SetsVariable: "name",
		Description: "Stores a value that later steps read as {{ vars.name }}.",
		Fields: []Field{
			{Key: "name", Label: "Variable", Type: "text", Required: true},
			{Key: "value", Label: "Value", Type: "text"},
		},
		Execute: func(_ context.Context, in *ExecInput) (Result, error) {
			name := strings.TrimSpace(in.String("name"))
			if name == "" {
				return Result{}, fmt.Errorf("variable name is required")
			}
			return Result{Output: map[string]any{"name": name, "value": in.Config["value"]}, Vars: map[string]any{name: in.Config["value"]}}, nil
		},
	})
	r.Register(NodeType{
		Type: "logic.compose", DisplayName: "Compose data", Category: CategoryLogic, Idempotent: true,
		Description: "Builds a value, parses or writes JSON, or selects a field.",
		Fields: []Field{
			{Key: "mode", Label: "Mode", Type: "select", Default: "compose", Options: []Option{
				{"compose", "Compose a value"}, {"json_parse", "Parse JSON text"}, {"json_encode", "Write as JSON text"}, {"select", "Select a field"},
			}},
			{Key: "value", Label: "Value", Type: "json"},
			{Key: "path", Label: "Field path", Type: "text", Help: "For Select, such as items.0.name."},
		},
		Execute: execCompose,
	})
	r.Register(NodeType{
		Type: "logic.stop", DisplayName: "Stop", Category: CategoryLogic, Idempotent: true,
		Description: "Ends the run here, as succeeded or failed.",
		Fields: []Field{
			{Key: "status", Label: "End as", Type: "select", Default: "succeeded", Options: []Option{{"succeeded", "Succeeded"}, {"failed", "Failed"}}},
			{Key: "message", Label: "Message", Type: "text"},
		},
		Execute: func(_ context.Context, in *ExecInput) (Result, error) {
			msg := in.String("message")
			return Result{Stop: true, StopFailed: in.String("status") == "failed", Message: msg, Output: map[string]any{"message": msg}}, nil
		},
	})
	r.Register(NodeType{
		Type: "logic.approval", DisplayName: "Wait for approval", Category: CategoryLogic,
		Description: "Pauses the run until someone allowed to run automations approves or rejects it on the run page.",
		Branches:    []string{"approved", "rejected"},
		Fields: []Field{
			{Key: "message", Label: "What to approve", Type: "textarea", Required: true},
		},
		Execute: func(_ context.Context, in *ExecInput) (Result, error) {
			return Result{WaitApproval: true, Output: map[string]any{"message": in.String("message")}}, nil
		},
	})
	httpFields := []Field{
		{Key: "method", Label: "Method", Type: "select", Default: "GET", Options: []Option{{"GET", "GET"}, {"POST", "POST"}, {"PUT", "PUT"}, {"PATCH", "PATCH"}, {"DELETE", "DELETE"}, {"HEAD", "HEAD"}}},
		{Key: "url", Label: "URL", Type: "text", Required: true, Placeholder: "https://example.com/api"},
		{Key: "headers", Label: "Headers", Type: "headers"},
		{Key: "query", Label: "Query", Type: "headers"},
		{Key: "body", Label: "Body", Type: "json"},
		{Key: "connection", Label: "Connection", Type: "connection", Help: "A saved credential. A token is sent as Authorization: Bearer."},
		{Key: "timeout_ms", Label: "Timeout (ms)", Type: "number", Default: 15000},
		{Key: "fail_on_status", Label: "Fail on HTTP 400 and above", Type: "boolean", Default: true},
		{Key: "allow_private_network", Label: "Allow private network addresses", Type: "boolean", Help: "Off by default so flows cannot reach internal services."},
	}
	r.Register(NodeType{
		Type: "action.http", DisplayName: "HTTP request", Category: CategoryAction, Idempotent: true, Action: true,
		Description: "Sends an HTTP request and returns the status and body.",
		Fields:      httpFields,
		Execute: func(ctx context.Context, in *ExecInput) (Result, error) {
			return execHTTP(ctx, in, deps, strings.ToUpper(firstText(in.String("method"), "GET")), in.Config["body"])
		},
		Preview: func(_ context.Context, in *ExecInput) (Preview, error) {
			return previewHTTP(in, deps, strings.ToUpper(firstText(in.String("method"), "GET"))), nil
		},
	})
	r.Register(NodeType{
		Type: "action.webhook", DisplayName: "Send webhook", Category: CategoryAction, Action: true,
		Description: "POSTs a JSON body to a URL.",
		Fields: []Field{
			{Key: "url", Label: "URL", Type: "text", Required: true},
			{Key: "body", Label: "JSON body", Type: "json"},
			{Key: "connection", Label: "Connection", Type: "connection"},
			{Key: "allow_private_network", Label: "Allow private network addresses", Type: "boolean"},
		},
		Execute: func(ctx context.Context, in *ExecInput) (Result, error) {
			body := in.Config["body"]
			if body == nil {
				body = map[string]any{"flow_id": in.FlowID, "run_id": in.RunID}
			}
			return execHTTP(ctx, in, deps, http.MethodPost, body)
		},
		Preview: func(_ context.Context, in *ExecInput) (Preview, error) {
			return previewHTTP(in, deps, http.MethodPost), nil
		},
	})
}

func firstText(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// EvalRule applies one condition rule.
func EvalRule(left any, op string, right any) (bool, error) {
	switch op {
	case "==", "!=", ">", ">=", "<", "<=":
		return expr.Compare(op, left, right)
	case "eq":
		return expr.Equal(left, right), nil
	case "neq":
		return !expr.Equal(left, right), nil
	case "contains", "not_contains":
		v, _ := expr.Call("contains", left, right)
		hit, _ := v.(bool)
		if op == "not_contains" {
			return !hit, nil
		}
		return hit, nil
	case "starts_with":
		v, _ := expr.Call("startsWith", left, right)
		hit, _ := v.(bool)
		return hit, nil
	case "is_empty", "not_empty":
		n, _ := expr.Call("length", left)
		empty := left == nil || n == float64(0)
		if op == "not_empty" {
			return !empty, nil
		}
		return empty, nil
	}
	return false, fmt.Errorf("unknown rule operator %q", op)
}

func execCondition(_ context.Context, in *ExecInput) (Result, error) {
	rules, _ := in.Config["rules"].([]any)
	if len(rules) > 0 {
		all := in.String("match") != "any"
		matched := all
		var results []RuleResult
		for i, raw := range rules {
			rule, _ := raw.(map[string]any)
			op := expr.Stringify(rule["op"])
			hit, err := EvalRule(rule["left"], op, rule["right"])
			if err != nil {
				return Result{}, fmt.Errorf("rule %d: %v", i+1, err)
			}
			results = append(results, RuleResult{Rule: fmt.Sprintf("rule %d", i+1), Left: rule["left"], Op: op, Right: rule["right"], Matched: hit})
			if all {
				matched = matched && hit
			} else {
				matched = matched || hit
			}
		}
		port := "false"
		if matched {
			port = "true"
		}
		return Result{Port: port, Rules: results, Output: map[string]any{"result": matched, "rules": rulesAny(results)}}, nil
	}
	src := strings.TrimSpace(in.String("expression"))
	if src == "" {
		return Result{}, fmt.Errorf("add a rule or an expression")
	}
	v, err := in.Eval(src)
	if err != nil {
		return Result{}, err
	}
	ok := expr.Truthy(v)
	port := "false"
	if ok {
		port = "true"
	}
	rr := []RuleResult{{Rule: flowdef.StripBraces(src), Left: v, Matched: ok}}
	return Result{Port: port, Rules: rr, Output: map[string]any{"result": ok, "value": v}}, nil
}

func rulesAny(rs []RuleResult) []any {
	out := make([]any, len(rs))
	for i, r := range rs {
		out[i] = map[string]any{"rule": r.Rule, "left": r.Left, "op": r.Op, "right": r.Right, "matched": r.Matched}
	}
	return out
}

// SwitchCases reads a switch's cases as (value, port) pairs.
func SwitchCases(v any) [][2]string {
	var out [][2]string
	list, _ := v.([]any)
	for _, c := range list {
		switch x := c.(type) {
		case string:
			out = append(out, [2]string{x, x})
		case map[string]any:
			val := expr.Stringify(x["value"])
			port := expr.Stringify(x["port"])
			if port == "" {
				port = val
			}
			out = append(out, [2]string{val, port})
		}
	}
	return out
}

func execSwitch(_ context.Context, in *ExecInput) (Result, error) {
	value := in.Config["value"]
	for _, c := range SwitchCases(in.Config["cases"]) {
		if expr.Equal(value, c[0]) {
			return Result{Port: c[1], Output: map[string]any{"value": value, "case": c[0]}}, nil
		}
	}
	return Result{Port: "default", Output: map[string]any{"value": value, "case": "default"}}, nil
}

// ParseDuration reads 30s, 10m, 2h, 1d or a number of milliseconds.
func ParseDuration(v any) (time.Duration, error) {
	switch x := expr.Normalize(v).(type) {
	case nil:
		return 0, nil
	case float64:
		return time.Duration(x) * time.Millisecond, nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, nil
		}
		if strings.HasSuffix(s, "d") {
			d, err := time.ParseDuration(strings.TrimSuffix(s, "d") + "h")
			return d * 24, err
		}
		if d, err := time.ParseDuration(s); err == nil {
			return d, nil
		}
		var ms int64
		if _, err := fmt.Sscanf(s, "%d", &ms); err == nil {
			return time.Duration(ms) * time.Millisecond, nil
		}
		return 0, fmt.Errorf("%q is not a duration such as 30s, 10m or 2h", s)
	}
	return 0, fmt.Errorf("duration must be text such as 10m")
}

func execDelay(_ context.Context, in *ExecInput) (Result, error) {
	if until := strings.TrimSpace(in.String("until")); until != "" {
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			return Result{}, fmt.Errorf("until must be a time such as 2026-01-01T09:00:00Z")
		}
		return Result{WaitUntil: t, Output: map[string]any{"until": t.UTC().Format(time.RFC3339)}, Would: "would wait until " + t.UTC().Format(time.RFC3339)}, nil
	}
	raw := in.Config["duration"]
	if raw == nil {
		raw = in.Config["duration_ms"]
	}
	d, err := ParseDuration(raw)
	if err != nil {
		return Result{}, err
	}
	if d <= 0 {
		return Result{}, fmt.Errorf("set how long to wait")
	}
	t := in.Now.Add(d)
	return Result{WaitUntil: t, Output: map[string]any{"until": t.UTC().Format(time.RFC3339), "seconds": d.Seconds()}, Would: "would wait " + d.String()}, nil
}

func execCompose(_ context.Context, in *ExecInput) (Result, error) {
	v := in.Config["value"]
	switch in.String("mode") {
	case "", "compose":
		return Result{Output: map[string]any{"value": v}}, nil
	case "json_parse":
		var out any
		if err := json.Unmarshal([]byte(expr.Stringify(v)), &out); err != nil {
			return Result{}, fmt.Errorf("value is not valid JSON: %v", err)
		}
		return Result{Output: map[string]any{"value": out}}, nil
	case "json_encode":
		raw, err := json.Marshal(v)
		if err != nil {
			return Result{}, err
		}
		return Result{Output: map[string]any{"value": string(raw)}}, nil
	case "select":
		return Result{Output: map[string]any{"value": expr.Lookup(v, in.String("path"))}}, nil
	}
	return Result{}, fmt.Errorf("mode must be compose, json_parse, json_encode or select")
}

func previewHTTP(in *ExecInput, deps BuiltinDeps, method string) Preview {
	target := in.String("url")
	p := Preview{Would: fmt.Sprintf("would send %s %s", method, target), Target: target}
	if _, err := url.ParseRequestURI(target); err != nil {
		p.Problems = append(p.Problems, "the URL is not valid")
		return p
	}
	if !in.Bool("allow_private_network") && deps.CheckURL != nil {
		if err := deps.CheckURL(target); err != nil {
			p.Problems = append(p.Problems, "the URL is refused: it must be a public http or https address unless private network addresses are allowed")
		}
	}
	if id := in.String("connection"); id != "" && in.Connection != nil {
		if _, err := in.Connection(id); err != nil {
			p.Problems = append(p.Problems, "connection: "+err.Error())
		}
	}
	return p
}

func execHTTP(ctx context.Context, in *ExecInput, deps BuiltinDeps, method string, body any) (Result, error) {
	target := in.String("url")
	u, err := url.ParseRequestURI(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Result{}, fmt.Errorf("the URL must start with http:// or https://")
	}
	if deps.CheckSelf != nil {
		if err := deps.CheckSelf(ctx, in, target); err != nil {
			return Result{}, err
		}
	}
	allowPrivate := in.Bool("allow_private_network")
	if !allowPrivate && deps.CheckURL != nil {
		if err := deps.CheckURL(target); err != nil {
			return Result{}, fmt.Errorf("the URL is refused: it must be a public address unless private network addresses are allowed")
		}
	}
	if q, ok := in.Config["query"].(map[string]any); ok && len(q) > 0 {
		vals := u.Query()
		for k, v := range q {
			vals.Set(k, expr.Stringify(v))
		}
		u.RawQuery = vals.Encode()
	}
	var reader io.Reader
	if body != nil && method != http.MethodGet && method != http.MethodHead {
		switch b := body.(type) {
		case string:
			reader = strings.NewReader(b)
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				return Result{}, fmt.Errorf("body cannot be written as JSON")
			}
			reader = bytes.NewReader(raw)
		}
	}
	timeout := time.Duration(in.Int("timeout_ms", 15000)) * time.Millisecond
	if timeout <= 0 || timeout > 2*time.Minute {
		timeout = 15 * time.Second
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, method, u.String(), reader)
	if err != nil {
		return Result{}, err
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "no-dal-automation")
	if h, ok := in.Config["headers"].(map[string]any); ok {
		for k, v := range h {
			req.Header.Set(k, expr.Stringify(v))
		}
	}
	if id := in.String("connection"); id != "" {
		if in.Connection == nil {
			return Result{}, fmt.Errorf("connections are unavailable")
		}
		conn, err := in.Connection(id)
		if err != nil {
			return Result{}, err
		}
		applyConnection(req, conn)
	}
	cli := http.DefaultClient
	if deps.HTTPClient != nil {
		cli = deps.HTTPClient(allowPrivate, timeout)
	}
	in.Log("info", "%s %s", method, u.Redacted())
	res, err := cli.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("request failed: %v", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, MaxResponseBytes+1))
	if err != nil {
		return Result{}, fmt.Errorf("reading the response: %v", err)
	}
	truncated := len(raw) > MaxResponseBytes
	if truncated {
		raw = raw[:MaxResponseBytes]
	}
	var parsed any
	if json.Unmarshal(raw, &parsed) != nil {
		parsed = string(raw)
	}
	headers := map[string]any{}
	keys := make([]string, 0, len(res.Header))
	for k := range res.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		headers[strings.ToLower(k)] = res.Header.Get(k)
	}
	out := map[string]any{"status": res.StatusCode, "headers": headers, "body": parsed, "truncated": truncated}
	failOnStatus := true
	if v, ok := in.Config["fail_on_status"].(bool); ok {
		failOnStatus = v
	}
	if failOnStatus && res.StatusCode >= 400 {
		return Result{Output: out}, fmt.Errorf("HTTP %d from %s", res.StatusCode, u.Host)
	}
	return Result{Output: out}, nil
}

// applyConnection adds a saved credential to a request.
func applyConnection(req *http.Request, conn map[string]any) {
	if tok := expr.Stringify(conn["token"]); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	user, pass := expr.Stringify(conn["username"]), expr.Stringify(conn["password"])
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	if h, ok := conn["headers"].(map[string]any); ok {
		for k, v := range h {
			req.Header.Set(k, expr.Stringify(v))
		}
	}
}
