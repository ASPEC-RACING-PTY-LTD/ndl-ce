package expr

import (
	"fmt"
	"strings"
)

// Template references are written {{ expression }} inside any string of a
// node's config. A string that is exactly one reference keeps the value's
// type (a list stays a list); otherwise each value is inserted as text.

// HasTemplate reports whether s contains a {{ }} reference.
func HasTemplate(s string) bool {
	return strings.Contains(s, "{{") && strings.Contains(s, "}}")
}

// TemplateExprs returns the expressions inside s, in order.
func TemplateExprs(s string) ([]string, error) {
	var out []string
	rest := s
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			return out, nil
		}
		end := strings.Index(rest[start+2:], "}}")
		if end < 0 {
			return out, fmt.Errorf("a {{ has no closing }}")
		}
		inner := strings.TrimSpace(rest[start+2 : start+2+end])
		if inner == "" {
			return out, fmt.Errorf("an empty {{ }} reference")
		}
		out = append(out, inner)
		rest = rest[start+2+end+2:]
	}
}

// CheckTemplate parses every reference in s.
func CheckTemplate(s string) ([]*Expr, error) {
	srcs, err := TemplateExprs(s)
	if err != nil {
		return nil, err
	}
	out := make([]*Expr, 0, len(srcs))
	for _, src := range srcs {
		e, err := Parse(src)
		if err != nil {
			return nil, fmt.Errorf("{{ %s }}: %v", src, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// RenderString renders one string.
func RenderString(s string, env map[string]any) (any, error) {
	if !HasTemplate(s) {
		return s, nil
	}
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") && strings.Count(trimmed, "{{") == 1 {
		return Eval(strings.TrimSpace(trimmed[2:len(trimmed)-2]), env)
	}
	var b strings.Builder
	rest := s
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			b.WriteString(rest)
			return b.String(), nil
		}
		end := strings.Index(rest[start+2:], "}}")
		if end < 0 {
			return nil, fmt.Errorf("a {{ has no closing }}")
		}
		b.WriteString(rest[:start])
		v, err := Eval(strings.TrimSpace(rest[start+2:start+2+end]), env)
		if err != nil {
			return nil, err
		}
		b.WriteString(Stringify(v))
		rest = rest[start+2+end+2:]
	}
}

// Render renders every string inside v (maps and lists are walked) and
// returns a new value; v is not changed.
func Render(v any, env map[string]any) (any, error) {
	switch x := v.(type) {
	case string:
		return RenderString(x, env)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			r, err := Render(item, env)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			r, err := Render(item, env)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = r
		}
		return out, nil
	}
	return v, nil
}
