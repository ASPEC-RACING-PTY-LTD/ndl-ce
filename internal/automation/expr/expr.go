// Package expr is the small, safe expression language used by automation
// flows. It has its own parser; nothing is ever passed to an evaluator of a
// general-purpose language. Expressions read data, compare it and call a
// fixed set of pure functions.
//
//	trigger.pool.used_percent >= 85 and not isNull(vars.owner)
//	contains(trigger.name, "web") or nodes.check.output.status == "down"
package expr

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Roots are the data roots an expression may read.
var Roots = []string{"trigger", "nodes", "vars", "inputs", "run", "item", "index"}

// Functions are the callable names, with their argument counts.
var Functions = map[string]int{
	"contains": 2, "startsWith": 2, "endsWith": 2, "isNull": 1, "length": 1, "lower": 1, "upper": 1,
}

// MaxLength bounds an expression's source so parsing stays cheap.
const MaxLength = 4096

// Expr is a parsed expression.
type Expr struct {
	src  string
	root node
}

// Source returns the text the expression was parsed from.
func (e *Expr) Source() string { return e.src }

// Paths lists every data reference in the expression, such as
// "nodes.check.output.status".
func (e *Expr) Paths() []string {
	var out []string
	walk(e.root, func(n node) {
		if p, ok := n.(*pathNode); ok {
			out = append(out, p.String())
		}
	})
	return out
}

// Parse compiles src. Unknown roots and functions are errors.
func Parse(src string) (*Expr, error) {
	if len(src) > MaxLength {
		return nil, fmt.Errorf("expression is longer than %d characters", MaxLength)
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	if len(toks) == 1 {
		return nil, fmt.Errorf("expression is empty")
	}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected %q at position %d", p.peek().text, p.peek().pos+1)
	}
	return &Expr{src: src, root: n}, nil
}

// Eval evaluates src against env.
func Eval(src string, env map[string]any) (any, error) {
	e, err := Parse(src)
	if err != nil {
		return nil, err
	}
	return e.Eval(env)
}

// Eval evaluates the expression against env, whose keys are the roots.
func (e *Expr) Eval(env map[string]any) (any, error) {
	return e.root.eval(env)
}

// Truthy reports how a value reads as a condition.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	if f, ok := toNumber(v); ok {
		return f != 0
	}
	return true
}

// --- lexer ---

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNumber
	tString
	tOp
	tLParen
	tRParen
	tLBrack
	tRBrack
	tDot
	tComma
)

type token struct {
	kind tokKind
	text string
	pos  int
}

func lex(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out = append(out, token{tLParen, "(", i})
			i++
		case c == ')':
			out = append(out, token{tRParen, ")", i})
			i++
		case c == '[':
			out = append(out, token{tLBrack, "[", i})
			i++
		case c == ']':
			out = append(out, token{tRBrack, "]", i})
			i++
		case c == '.':
			out = append(out, token{tDot, ".", i})
			i++
		case c == ',':
			out = append(out, token{tComma, ",", i})
			i++
		case c == '"' || c == '\'':
			s, n, err := lexString(src[i:])
			if err != nil {
				return nil, fmt.Errorf("%v at position %d", err, i+1)
			}
			out = append(out, token{tString, s, i})
			i += n
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				j++
			}
			out = append(out, token{tNumber, src[i:j], i})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			out = append(out, token{tIdent, src[i:j], i})
			i = j
		default:
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "==", "!=", "<=", ">=", "&&", "||":
				out = append(out, token{tOp, two, i})
				i += 2
				continue
			}
			switch c {
			case '<', '>', '!', '-':
				out = append(out, token{tOp, string(c), i})
				i++
				continue
			}
			return nil, fmt.Errorf("unexpected character %q at position %d", c, i+1)
		}
	}
	out = append(out, token{tEOF, "", len(src)})
	return out, nil
}

func lexString(s string) (string, int, error) {
	quote := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		if c == quote {
			return b.String(), i + 1, nil
		}
		b.WriteByte(c)
	}
	return "", 0, fmt.Errorf("unterminated string")
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9' || c == '-'
}

// --- parser ---

type parser struct {
	toks []token
	i    int
}

func (p *parser) peek() token { return p.toks[p.i] }
func (p *parser) next() token {
	t := p.toks[p.i]
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}

func (p *parser) isWord(w string) bool {
	t := p.peek()
	return t.kind == tIdent && t.text == w
}

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isWord("or") || (p.peek().kind == tOp && p.peek().text == "||") {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &logicNode{op: "or", l: left, r: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isWord("and") || (p.peek().kind == tOp && p.peek().text == "&&") {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &logicNode{op: "and", l: left, r: right}
	}
	return left, nil
}

func (p *parser) parseNot() (node, error) {
	if p.isWord("not") || (p.peek().kind == tOp && p.peek().text == "!") {
		p.next()
		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &notNode{inner}, nil
	}
	return p.parseCmp()
}

var cmpOps = map[string]bool{"==": true, "!=": true, "<": true, ">": true, "<=": true, ">=": true}

func (p *parser) parseCmp() (node, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t.kind == tOp && cmpOps[t.text] {
		p.next()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &cmpNode{op: t.text, l: left, r: right}, nil
	}
	// Infix forms read naturally in conditions: a contains "x".
	if t.kind == tIdent && (t.text == "contains" || t.text == "startsWith" || t.text == "endsWith") {
		p.next()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &callNode{name: t.text, args: []node{left, right}}, nil
	}
	return left, nil
}

func (p *parser) parsePrimary() (node, error) {
	t := p.next()
	switch t.kind {
	case tNumber:
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, fmt.Errorf("bad number %q", t.text)
		}
		return &litNode{f}, nil
	case tString:
		return &litNode{t.text}, nil
	case tOp:
		if t.text == "-" {
			n := p.next()
			if n.kind != tNumber {
				return nil, fmt.Errorf("expected a number after - at position %d", t.pos+1)
			}
			f, err := strconv.ParseFloat(n.text, 64)
			if err != nil {
				return nil, fmt.Errorf("bad number %q", n.text)
			}
			return &litNode{-f}, nil
		}
	case tLParen:
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.next().kind != tRParen {
			return nil, fmt.Errorf("missing ) for ( at position %d", t.pos+1)
		}
		return inner, nil
	case tIdent:
		switch t.text {
		case "true":
			return &litNode{true}, nil
		case "false":
			return &litNode{false}, nil
		case "null":
			return &litNode{nil}, nil
		}
		if p.peek().kind == tLParen {
			argc, ok := Functions[t.text]
			if !ok {
				return nil, fmt.Errorf("unknown function %s", t.text)
			}
			p.next()
			var args []node
			if p.peek().kind != tRParen {
				for {
					a, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.peek().kind == tComma {
						p.next()
						continue
					}
					break
				}
			}
			if p.next().kind != tRParen {
				return nil, fmt.Errorf("missing ) after %s arguments", t.text)
			}
			if len(args) != argc {
				return nil, fmt.Errorf("%s takes %d argument(s), got %d", t.text, argc, len(args))
			}
			return &callNode{name: t.text, args: args}, nil
		}
		if !isRoot(t.text) {
			return nil, fmt.Errorf("unknown name %s; data starts with %s", t.text, strings.Join(Roots, ", "))
		}
		pn := &pathNode{parts: []any{t.text}}
		for {
			switch p.peek().kind {
			case tDot:
				p.next()
				seg := p.next()
				if seg.kind != tIdent && seg.kind != tNumber {
					return nil, fmt.Errorf("expected a name after . at position %d", seg.pos+1)
				}
				pn.parts = append(pn.parts, seg.text)
				continue
			case tLBrack:
				p.next()
				seg := p.next()
				switch seg.kind {
				case tNumber:
					n, err := strconv.Atoi(seg.text)
					if err != nil {
						return nil, fmt.Errorf("bad index %q", seg.text)
					}
					pn.parts = append(pn.parts, n)
				case tString:
					pn.parts = append(pn.parts, seg.text)
				default:
					return nil, fmt.Errorf("expected an index at position %d", seg.pos+1)
				}
				if p.next().kind != tRBrack {
					return nil, fmt.Errorf("missing ]")
				}
				continue
			}
			break
		}
		return pn, nil
	case tEOF:
		return nil, fmt.Errorf("expression ends early")
	}
	return nil, fmt.Errorf("unexpected %q at position %d", t.text, t.pos+1)
}

func isRoot(s string) bool {
	for _, r := range Roots {
		if r == s {
			return true
		}
	}
	return false
}

// --- AST ---

type node interface {
	eval(env map[string]any) (any, error)
}

func walk(n node, fn func(node)) {
	fn(n)
	switch x := n.(type) {
	case *logicNode:
		walk(x.l, fn)
		walk(x.r, fn)
	case *notNode:
		walk(x.inner, fn)
	case *cmpNode:
		walk(x.l, fn)
		walk(x.r, fn)
	case *callNode:
		for _, a := range x.args {
			walk(a, fn)
		}
	}
}

type litNode struct{ v any }

func (n *litNode) eval(map[string]any) (any, error) { return n.v, nil }

type pathNode struct{ parts []any }

func (n *pathNode) String() string {
	var b strings.Builder
	for i, p := range n.parts {
		switch x := p.(type) {
		case int:
			fmt.Fprintf(&b, "[%d]", x)
		case string:
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(x)
		}
	}
	return b.String()
}

func (n *pathNode) eval(env map[string]any) (any, error) {
	var cur any = env
	for _, p := range n.parts {
		cur = index(cur, p)
		if cur == nil {
			return nil, nil
		}
	}
	return Normalize(cur), nil
}

// Lookup reads a dotted path such as "pool.name" from v. Missing keys are nil.
func Lookup(v any, path string) any {
	if strings.TrimSpace(path) == "" {
		return v
	}
	cur := v
	for _, seg := range strings.Split(path, ".") {
		if n, err := strconv.Atoi(seg); err == nil {
			if _, isList := Normalize(cur).([]any); isList {
				cur = index(cur, n)
				continue
			}
		}
		cur = index(cur, seg)
		if cur == nil {
			return nil
		}
	}
	return Normalize(cur)
}

func index(v any, key any) any {
	switch c := Normalize(v).(type) {
	case map[string]any:
		if k, ok := key.(string); ok {
			return c[k]
		}
		if k, ok := key.(int); ok {
			return c[strconv.Itoa(k)]
		}
	case []any:
		k, ok := key.(int)
		if !ok {
			if s, isStr := key.(string); isStr {
				n, err := strconv.Atoi(s)
				if err != nil {
					return nil
				}
				k = n
			} else {
				return nil
			}
		}
		if k < 0 || k >= len(c) {
			return nil
		}
		return c[k]
	}
	return nil
}

// Normalize turns typed maps and slices into map[string]any and []any,
// and integer kinds into float64, so evaluation sees one shape.
func Normalize(v any) any {
	switch x := v.(type) {
	case map[string]string:
		out := make(map[string]any, len(x))
		for k, s := range x {
			out[k] = s
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	case []map[string]any:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case float32:
		return float64(x)
	}
	return v
}

type logicNode struct {
	op   string
	l, r node
}

func (n *logicNode) eval(env map[string]any) (any, error) {
	l, err := n.l.eval(env)
	if err != nil {
		return nil, err
	}
	if n.op == "and" && !Truthy(l) {
		return false, nil
	}
	if n.op == "or" && Truthy(l) {
		return true, nil
	}
	r, err := n.r.eval(env)
	if err != nil {
		return nil, err
	}
	return Truthy(r), nil
}

type notNode struct{ inner node }

func (n *notNode) eval(env map[string]any) (any, error) {
	v, err := n.inner.eval(env)
	if err != nil {
		return nil, err
	}
	return !Truthy(v), nil
}

type cmpNode struct {
	op   string
	l, r node
}

func (n *cmpNode) eval(env map[string]any) (any, error) {
	l, err := n.l.eval(env)
	if err != nil {
		return nil, err
	}
	r, err := n.r.eval(env)
	if err != nil {
		return nil, err
	}
	return Compare(n.op, l, r)
}

// Compare applies a comparison operator. Numbers compare numerically, also
// when one side is text that reads as a number (event payloads are text).
func Compare(op string, l, r any) (bool, error) {
	l, r = Normalize(l), Normalize(r)
	switch op {
	case "==":
		return Equal(l, r), nil
	case "!=":
		return !Equal(l, r), nil
	}
	lf, lok := toNumber(l)
	rf, rok := toNumber(r)
	if lok && rok {
		switch op {
		case "<":
			return lf < rf, nil
		case ">":
			return lf > rf, nil
		case "<=":
			return lf <= rf, nil
		case ">=":
			return lf >= rf, nil
		}
	}
	ls, lsok := l.(string)
	rs, rsok := r.(string)
	if lsok && rsok {
		switch op {
		case "<":
			return ls < rs, nil
		case ">":
			return ls > rs, nil
		case "<=":
			return ls <= rs, nil
		case ">=":
			return ls >= rs, nil
		}
	}
	if l == nil || r == nil {
		return false, nil
	}
	return false, fmt.Errorf("cannot compare %s %s %s", typeName(l), op, typeName(r))
}

// Equal is loose equality: 85 equals "85", nil equals only nil.
func Equal(l, r any) bool {
	l, r = Normalize(l), Normalize(r)
	if l == nil || r == nil {
		return l == nil && r == nil
	}
	if lf, ok := toNumber(l); ok {
		if rf, ok := toNumber(r); ok {
			return lf == rf
		}
	}
	switch lv := l.(type) {
	case string:
		rv, ok := r.(string)
		return ok && lv == rv
	case bool:
		rv, ok := r.(bool)
		if !ok {
			if s, isStr := r.(string); isStr {
				return strconv.FormatBool(lv) == strings.ToLower(s)
			}
		}
		return ok && lv == rv
	}
	return Stringify(l) == Stringify(r)
}

func toNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "text"
	case bool:
		return "true/false"
	case float64:
		return "number"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

type callNode struct {
	name string
	args []node
}

func (n *callNode) eval(env map[string]any) (any, error) {
	vals := make([]any, len(n.args))
	for i, a := range n.args {
		v, err := a.eval(env)
		if err != nil {
			return nil, err
		}
		vals[i] = v
	}
	return Call(n.name, vals...)
}

// Call runs one of the fixed functions.
func Call(name string, args ...any) (any, error) {
	switch name {
	case "contains":
		switch c := Normalize(args[0]).(type) {
		case string:
			return strings.Contains(c, Stringify(args[1])), nil
		case []any:
			for _, item := range c {
				if Equal(item, args[1]) {
					return true, nil
				}
			}
			return false, nil
		case map[string]any:
			_, ok := c[Stringify(args[1])]
			return ok, nil
		case nil:
			return false, nil
		}
		return strings.Contains(Stringify(args[0]), Stringify(args[1])), nil
	case "startsWith":
		return args[0] != nil && strings.HasPrefix(Stringify(args[0]), Stringify(args[1])), nil
	case "endsWith":
		return args[0] != nil && strings.HasSuffix(Stringify(args[0]), Stringify(args[1])), nil
	case "isNull":
		return Normalize(args[0]) == nil, nil
	case "length":
		switch c := Normalize(args[0]).(type) {
		case nil:
			return float64(0), nil
		case string:
			return float64(len([]rune(c))), nil
		case []any:
			return float64(len(c)), nil
		case map[string]any:
			return float64(len(c)), nil
		}
		return float64(len(Stringify(args[0]))), nil
	case "lower":
		return strings.ToLower(Stringify(args[0])), nil
	case "upper":
		return strings.ToUpper(Stringify(args[0])), nil
	}
	return nil, fmt.Errorf("unknown function %s", name)
}

// Stringify renders a value for text templates.
func Stringify(v any) string {
	switch x := Normalize(v).(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, strconv.Quote(k)+":"+jsonish(x[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, jsonish(item))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	return fmt.Sprint(v)
}

func jsonish(v any) string {
	if s, ok := Normalize(v).(string); ok {
		return strconv.Quote(s)
	}
	if v == nil {
		return "null"
	}
	return Stringify(v)
}
