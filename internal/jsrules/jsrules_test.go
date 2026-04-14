package jsrules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// writeRule is a small helper that drops a manifest + main.js pair into a
// fresh tempdir. Used to keep each test self-contained without reaching
// for the real rules-js/ tree.
func writeRule(t *testing.T, id, severity, js string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"` + id + `","description":"t","severity":"` + severity + `","requiresSchema":false,"requiresExplain":false}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadAndInvoke(t *testing.T) {
	root := writeRule(t, "t_basic", "warn", `
        function check(ctx) {
            return [pg.finding("hello", "do something", 0, 1)];
        }
    `)
	engine, err := LoadDir(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := len(engine.Rules()); got != 1 {
		t.Fatalf("want 1 rule, got %d", got)
	}
	r := engine.Rules()[0]
	if r.ID() != "t_basic" || r.DefaultSeverity() != analyzer.SeverityWarn {
		t.Errorf("unexpected rule metadata: %s / %s", r.ID(), r.DefaultSeverity())
	}
	ast, err := analyzer.ParseJSON("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	findings := r.Check(&analyzer.Context{Source: "SELECT 1", AST: ast})
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	if findings[0].Message != "hello" {
		t.Errorf("unexpected message: %q", findings[0].Message)
	}
	if findings[0].Rule != "t_basic" {
		t.Errorf("rule id not stamped on finding: %q", findings[0].Rule)
	}
}

func TestHelpersExposed(t *testing.T) {
	root := writeRule(t, "t_helpers", "info", `
        function check(ctx) {
            var stmts = [];
            pg.forEachStmt(ctx.ast, function (kind) { stmts.push(kind); });
            if (stmts.length !== 1 || stmts[0] !== "SelectStmt") {
                return [pg.finding("forEachStmt wrong: " + stmts.join(","))];
            }
            var tables = pg.tableRefs(ctx.ast);
            if (tables.length !== 1 || tables[0] !== "users") {
                return [pg.finding("tableRefs wrong: " + tables.join(","))];
            }
            var cols = pg.columnRefs(ctx.ast);
            if (cols.indexOf("id") < 0) {
                return [pg.finding("columnRefs missing id: " + cols.join(","))];
            }
            return [];
        }
    `)
	engine, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	ast, err := analyzer.ParseJSON("SELECT id FROM users")
	if err != nil {
		t.Fatal(err)
	}
	findings := engine.Rules()[0].Check(&analyzer.Context{Source: "SELECT id FROM users", AST: ast})
	if len(findings) != 0 {
		t.Errorf("helpers test produced findings (should be silent): %+v", findings)
	}
}

func TestRuntimeErrorSurfacesAsFinding(t *testing.T) {
	root := writeRule(t, "t_boom", "warn", `
        function check(ctx) { throw new Error("boom"); }
    `)
	engine, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	ast, _ := analyzer.ParseJSON("SELECT 1")
	findings := engine.Rules()[0].Check(&analyzer.Context{Source: "SELECT 1", AST: ast})
	if len(findings) != 1 {
		t.Fatalf("want 1 diagnostic finding, got %d", len(findings))
	}
	if findings[0].Severity != analyzer.SeverityInfo {
		t.Errorf("runtime error should be reported as info, got %s", findings[0].Severity)
	}
}

func TestManifestValidation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bad")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"id":"","description":"x","severity":"warn"}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "main.js"), []byte(`function check(){return [];}`), 0o644)
	if _, err := LoadDir(root); err == nil {
		t.Fatal("expected load to fail on empty id")
	}
}
