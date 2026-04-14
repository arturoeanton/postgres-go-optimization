// Integration test: read every testdata/bad/*.sql file, confirm the
// expected rule fires; read every testdata/good/*.sql, confirm no
// findings at warn or error.
//
// Run: go test ./...
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
	_ "github.com/arturoeanton/postgres-go-optimization/internal/rules" // register rules
	"github.com/arturoeanton/postgres-go-optimization/internal/rules"
)

func TestBadFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/bad/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no fixtures")
	}
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			src := string(b)
			expectedRule := parseExpected(src)
			if expectedRule == "" {
				t.Skipf("%s: no -- expect: header", file)
			}
			ast, err := analyzer.ParseJSON(src)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &analyzer.Context{Source: src, AST: ast}
			all := rules.All()
			runnables := make([]analyzer.Runnable, 0, len(all))
			for _, r := range all {
				runnables = append(runnables, r)
			}
			found := analyzer.Run(ctx, runnables)
			fired := false
			for _, f := range found {
				if f.Rule == expectedRule {
					fired = true
					break
				}
			}
			if !fired {
				t.Errorf("expected rule %q not fired. got %d findings:", expectedRule, len(found))
				for _, f := range found {
					t.Logf("  - %s: %s", f.Rule, f.Message)
				}
			}
		})
	}
}

func TestGoodFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/good/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			src := string(b)
			ast, err := analyzer.ParseJSON(src)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &analyzer.Context{Source: src, AST: ast}
			all := rules.All()
			runnables := make([]analyzer.Runnable, 0, len(all))
			for _, r := range all {
				// skip schema/explain rules in this offline test
				if r.RequiresSchema() || r.RequiresExplain() {
					continue
				}
				runnables = append(runnables, r)
			}
			found := analyzer.Run(ctx, runnables)
			for _, f := range found {
				if f.Severity >= analyzer.SeverityWarn {
					t.Errorf("expected clean, got %s: %s [%s]", f.Severity, f.Message, f.Rule)
				}
			}
		})
	}
}

// parseExpected reads the first line looking for "-- expect: rule_name".
func parseExpected(src string) string {
	for _, line := range strings.SplitN(src, "\n", 5) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-- expect:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "-- expect:"))
		if v == "(none)" {
			return ""
		}
		return v
	}
	return ""
}
