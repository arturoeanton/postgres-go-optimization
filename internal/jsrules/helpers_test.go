package jsrules

import (
	"os"
	"testing"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// TestHelpersSurface loads a rule that exercises every pg.* helper and
// fails loudly if any of them misbehaves. The assertions live inside
// the JS so a broken helper surfaces as a finding whose message names
// the failing check.
func TestHelpersSurface(t *testing.T) {
	root := writeRule(t, "helpers_surface", "warn", `
        function check(ctx) {
            const fail = [];

            // nodeKind / inner / as* on a wrapped node
            const first = pg.asList(ctx.ast, "stmts")[0];
            const stmtWrap = pg.asMap(first, "stmt");
            if (pg.nodeKind(stmtWrap) !== "SelectStmt") fail.push("nodeKind");
            const inner = pg.inner(stmtWrap);
            if (!inner) fail.push("inner");

            // forEachStmt
            let seen = 0;
            pg.forEachStmt(ctx.ast, function(kind) {
                if (kind === "SelectStmt") seen++;
            });
            if (seen !== 1) fail.push("forEachStmt seen=" + seen);

            // forEachSelect (there is exactly one SelectStmt)
            let selects = 0;
            pg.forEachSelect(ctx.ast, function() { selects++; });
            if (selects !== 1) fail.push("forEachSelect selects=" + selects);

            // findNodes
            const cols = pg.findNodes(ctx.ast, "ColumnRef");
            if (!cols || cols.length === 0) fail.push("findNodes");

            // columnRefs
            const refs = pg.columnRefs(ctx.ast);
            if (refs.indexOf("id") < 0) fail.push("columnRefs");

            // tableRefs
            const tables = pg.tableRefs(ctx.ast);
            if (tables[0] !== "users") fail.push("tableRefs=" + tables.join(","));

            // walk with pruning
            let visited = 0;
            pg.walk(ctx.ast, function(path, node) {
                visited++;
                return pg.nodeKind(node) !== "ColumnRef"; // prune at ColumnRef
            });
            if (visited === 0) fail.push("walk never visited");

            // firstLocation on the whole tree should be >= 0
            const loc = pg.firstLocation(ctx.ast);
            if (loc < 0) fail.push("firstLocation=" + loc);

            // asInt / asString wrong-type fallbacks
            const bogus = {};
            if (pg.asInt(bogus, "k") !== 0) fail.push("asInt default");
            if (pg.asString(bogus, "k") !== "") fail.push("asString default");

            if (fail.length > 0) {
                return [pg.finding("helper surface broken: " + fail.join(", "))];
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
	findings := engine.Rules()[0].Check(&analyzer.Context{
		Source: "SELECT id FROM users",
		AST:    ast,
	})
	if len(findings) > 0 {
		t.Errorf("expected silent run, got: %s", findings[0].Message)
	}
}

// TestCoerceFindings_Variants checks that the JS→Go finding coercion
// accepts the common shapes (array, single object) and rejects obvious
// garbage without crashing.
func TestCoerceFindings_Variants(t *testing.T) {
	// single-object return
	root := writeRule(t, "single_obj", "warn", `
        function check() {
            return { message: "one", location: { start: 0, end: 1 } };
        }
    `)
	engine, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	ast, _ := analyzer.ParseJSON("SELECT 1")
	got := engine.Rules()[0].Check(&analyzer.Context{Source: "SELECT 1", AST: ast})
	if len(got) != 1 || got[0].Message != "one" {
		t.Errorf("single-object coercion failed: %+v", got)
	}

	// severity override from JS
	root2 := writeRule(t, "sev_override", "info", `
        function check() {
            return [{ message: "err", severity: "error", location: { start: 0, end: 0 } }];
        }
    `)
	eng2, _ := LoadDir(root2)
	got = eng2.Rules()[0].Check(&analyzer.Context{Source: "SELECT 1", AST: ast})
	if len(got) != 1 || got[0].Severity != analyzer.SeverityError {
		t.Errorf("severity override lost: %+v", got)
	}

	// messageless object should be dropped
	root3 := writeRule(t, "no_message", "warn", `
        function check() { return [{ suggestion: "x" }]; }
    `)
	eng3, _ := LoadDir(root3)
	got = eng3.Rules()[0].Check(&analyzer.Context{Source: "SELECT 1", AST: ast})
	if len(got) != 0 {
		t.Errorf("findings without message should be dropped: %+v", got)
	}

	// garbage return (a number) should not crash
	root4 := writeRule(t, "garbage", "warn", `
        function check() { return 42; }
    `)
	eng4, _ := LoadDir(root4)
	got = eng4.Rules()[0].Check(&analyzer.Context{Source: "SELECT 1", AST: ast})
	if len(got) != 0 {
		t.Errorf("garbage return should coerce to empty: %+v", got)
	}
}

func TestRule_MetadataAccessors(t *testing.T) {
	root := writeRule(t, "m_explain", "info", `function check(){return [];}`)
	// Overwrite the manifest with requiresSchema + requiresExplain true.
	path := root + "/m_explain/manifest.json"
	if err := writeFile(t, path, `{"id":"m_explain","description":"d","severity":"info","requiresSchema":true,"requiresExplain":true}`); err != nil {
		t.Fatal(err)
	}
	eng, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	r := eng.Rules()[0]
	if r.Description() != "d" {
		t.Errorf("Description: %q", r.Description())
	}
	if !r.RequiresSchema() || !r.RequiresExplain() {
		t.Error("Requires flags did not round-trip through manifest")
	}
}

func TestLoadDir_NotDir(t *testing.T) {
	if _, err := LoadDir("/etc/hosts"); err == nil {
		t.Error("loading a regular file should error")
	}
}

func TestLoadDir_MissingDir(t *testing.T) {
	if _, err := LoadDir("/nonexistent/x/y/z"); err == nil {
		t.Error("missing directory should error")
	}
}

func TestLoadDir_IgnoresNonRuleSubdirs(t *testing.T) {
	root := writeRule(t, "real_rule", "warn", `function check(){return [];}`)
	// Add a subdir without a manifest — should be silently skipped.
	if err := mkdir(t, root+"/scratch"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(t, root+"/scratch/README.md", "notes"); err != nil {
		t.Fatal(err)
	}
	eng, err := LoadDir(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(eng.Rules()) != 1 {
		t.Errorf("subdir without manifest should be ignored; got %d rules", len(eng.Rules()))
	}
}

func writeFile(t *testing.T, path, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0o644)
}
func mkdir(t *testing.T, path string) error {
	t.Helper()
	return os.MkdirAll(path, 0o755)
}
