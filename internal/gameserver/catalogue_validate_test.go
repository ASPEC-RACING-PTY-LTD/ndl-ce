package gameserver

import (
	"strings"
	"testing"
)

// TestEveryBuiltinTemplateValidates is the catalogue gate: schema, unique
// IDs, installers, runtimes, ports, startup placeholders and resources.
func TestEveryBuiltinTemplateValidates(t *testing.T) {
	seen := map[string]bool{}
	var problems []string
	for _, tmpl := range BuiltinTemplates() {
		if seen[tmpl.ID] {
			problems = append(problems, tmpl.ID+": duplicate id")
		}
		seen[tmpl.ID] = true
		for _, err := range ValidateTemplate(tmpl) {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		t.Fatalf("%d catalogue problems:\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

// TestCatalogueStats logs the catalogue shape (run with -v) and guards
// against an accidental mass removal.
func TestCatalogueStats(t *testing.T) {
	all := BuiltinTemplates()
	byInstaller, byCategory, byLevel := map[string]int{}, map[string]int{}, map[string]int{}
	visible := 0
	for _, tmpl := range all {
		if tmpl.Hidden {
			continue
		}
		visible++
		byInstaller[tmpl.InstallBuiltin]++
		byCategory[tmpl.Category]++
		byLevel[VerificationLevel(tmpl)]++
	}
	t.Logf("templates: %d total, %d in catalogue", len(all), visible)
	t.Logf("by installer: %v", byInstaller)
	t.Logf("by category: %v", byCategory)
	t.Logf("by verification: %v", byLevel)
	if visible < 100 {
		t.Fatalf("catalogue unexpectedly small: %d", visible)
	}
}
