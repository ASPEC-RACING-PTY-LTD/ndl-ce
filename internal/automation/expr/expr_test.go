package expr

import (
	"reflect"
	"strings"
	"testing"
)

func testEnv() map[string]any {
	return map[string]any{
		"trigger": map[string]any{
			"name": "web-1", "used_percent": "92", "tags": []any{"prod", "web"},
			"pool": map[string]any{"name": "fast"},
		},
		"nodes": map[string]any{"check": map[string]any{"output": map[string]any{"status": "down", "code": 503}}},
		"vars":  map[string]any{"count": 3, "owner": nil},
		"run":   map[string]any{"id": "r1"},
	}
}

func TestEvalOperatorsAndPrecedence(t *testing.T) {
	env := testEnv()
	cases := map[string]any{
		`trigger.used_percent >= 85`:                         true,
		`trigger.used_percent > 95`:                          false,
		`nodes.check.output.status == "down"`:                true,
		`nodes.check.output.code != 200 and vars.count < 5`:  true,
		`true or false and false`:                            true, // and binds tighter
		`(true or false) and false`:                          false,
		`not isNull(vars.owner)`:                             false,
		`isNull(vars.missing)`:                               true,
		`contains(trigger.tags, "web")`:                      true,
		`trigger.name contains "web"`:                        true,
		`startsWith(trigger.name, "web")`:                    true,
		`length(trigger.tags) == 2`:                          true,
		`trigger.pool.name`:                                  "fast",
		`trigger.tags[1]`:                                    "web",
		`vars.count`:                                         float64(3),
		`-1 < 0`:                                             true,
		`!false && true`:                                     true,
		`lower("ABC") == 'abc'`:                              true,
		`trigger.missing.deeper == null`:                     true,
		`nodes.check.output.code >= 500 or trigger.x == "y"`: true,
	}
	for src, want := range cases {
		got, err := Eval(src, env)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %#v, want %#v", src, got, want)
		}
	}
}

func TestParseRejectsUnsafeOrBadInput(t *testing.T) {
	bad := []string{
		``, `os.exit(1)`, `eval("x")`, `trigger.a ==`, `(true`, `"unterminated`,
		`contains(trigger.a)`, `trigger.a ; rm`, `secrets.key`,
	}
	for _, src := range bad {
		if _, err := Parse(src); err == nil {
			t.Fatalf("%q parsed", src)
		}
	}
	if _, err := Parse(strings.Repeat("a", MaxLength+1)); err == nil {
		t.Fatal("too long")
	}
}

func TestPaths(t *testing.T) {
	e, err := Parse(`nodes.a.output.x == 1 and contains(vars.list, trigger.name)`)
	if err != nil {
		t.Fatal(err)
	}
	got := e.Paths()
	want := []string{"nodes.a.output.x", "vars.list", "trigger.name"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestTemplatesKeepTypeForSingleReference(t *testing.T) {
	env := testEnv()
	v, err := RenderString("{{ trigger.tags }}", env)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.([]any); !ok {
		t.Fatalf("single reference must keep its type: %#v", v)
	}
	s, err := RenderString("Pool {{ trigger.pool.name }} is at {{trigger.used_percent}}%", env)
	if err != nil || s != "Pool fast is at 92%" {
		t.Fatalf("%v %v", s, err)
	}
	out, err := Render(map[string]any{"body": map[string]any{"n": "{{ vars.count }}"}, "list": []any{"{{ run.id }}"}}, env)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["body"].(map[string]any)["n"] != float64(3) || m["list"].([]any)[0] != "r1" {
		t.Fatalf("%#v", out)
	}
	if _, err := CheckTemplate("{{ nope( }}"); err == nil {
		t.Fatal("bad template")
	}
	if _, err := CheckTemplate("{{ trigger.a"); err == nil {
		t.Fatal("unclosed template")
	}
}
