// Integration test for JS rules: every testdata/bad-js/*.sql fixture
// declares an expected JS rule id via `-- expect: <id>`. This test loads
// the rules-js/ tree, runs the combined Go+JS rule set, and asserts the
// expected rule fired for each fixture.
//
// Run: go test ./...
package main_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
	"github.com/arturoeanton/postgres-go-optimization/internal/jsrules"
	_ "github.com/arturoeanton/postgres-go-optimization/internal/rules" // register Go rules
	"github.com/arturoeanton/postgres-go-optimization/internal/rules"
)

func TestBadFixturesJS(t *testing.T) {
	files, err := filepath.Glob("testdata/bad-js/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no js fixtures")
	}
	engine, err := jsrules.LoadDir("rules-js")
	if err != nil {
		t.Fatalf("load js rules: %v", err)
	}
	runnables := buildRunnables(engine)
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			src := string(b)
			expected := parseExpected(src)
			if expected == "" {
				t.Skipf("%s: no -- expect: header", file)
			}
			ast, err := analyzer.ParseJSON(src)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &analyzer.Context{Source: src, AST: ast}
			found := analyzer.Run(ctx, runnables)
			for _, f := range found {
				if f.Rule == expected {
					return
				}
			}
			t.Errorf("expected rule %q not fired. got %d findings:", expected, len(found))
			for _, f := range found {
				t.Logf("  - %s: %s", f.Rule, f.Message)
			}
		})
	}
}

// TestJSRulesLoad sanity-checks that every directory under rules-js/
// loads cleanly. This catches manifest typos and JS syntax errors before
// they reach the fixture tests.
func TestJSRulesLoad(t *testing.T) {
	engine, err := jsrules.LoadDir("rules-js")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(engine.Rules()) == 0 {
		t.Fatal("expected at least one JS rule, got 0")
	}
	seen := map[string]bool{}
	for _, r := range engine.Rules() {
		if seen[r.ID()] {
			t.Errorf("duplicate rule id from loader: %s", r.ID())
		}
		seen[r.ID()] = true
		if r.Description() == "" {
			t.Errorf("%s: missing description", r.ID())
		}
	}
}

func buildRunnables(engine *jsrules.Engine) []analyzer.Runnable {
	var out []analyzer.Runnable
	for _, r := range rules.All() {
		out = append(out, r)
	}
	for _, r := range engine.Rules() {
		out = append(out, r)
	}
	return out
}
