package analyzer

import "testing"

func TestParseJSON_Valid(t *testing.T) {
	ast, err := ParseJSON("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if ast == nil {
		t.Fatal("ast nil")
	}
	stmts, ok := ast["stmts"].([]any)
	if !ok || len(stmts) != 1 {
		t.Fatalf("expected 1 stmt, got %v", ast["stmts"])
	}
}

func TestParseJSON_Invalid(t *testing.T) {
	_, err := ParseJSON("SELEKT 1 FROM where")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestWalk_VisitsAllNodes(t *testing.T) {
	ast, err := ParseJSON("SELECT a FROM t WHERE a = 1")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	Walk(ast, func(_ []string, n ASTNode) bool {
		if k := NodeKind(n); k != "" {
			seen[k] = true
		}
		return true
	})
	required := []string{"SelectStmt", "ColumnRef", "RangeVar", "A_Expr", "A_Const"}
	for _, r := range required {
		if !seen[r] {
			t.Errorf("expected to see node kind %q; seen: %v", r, seen)
		}
	}
}

func TestWalk_StopDescent(t *testing.T) {
	ast, _ := ParseJSON("SELECT (SELECT 1) FROM t")
	subCount := 0
	Walk(ast, func(_ []string, n ASTNode) bool {
		if NodeKind(n) == "SubLink" {
			subCount++
			return false // don't descend
		}
		return true
	})
	if subCount == 0 {
		t.Error("expected to find at least one SubLink")
	}
}

func TestNodeKindAndInner(t *testing.T) {
	n := ASTNode{"SelectStmt": ASTNode{"op": "SETOP_NONE"}}
	if NodeKind(n) != "SelectStmt" {
		t.Fatal("NodeKind wrong")
	}
	if Inner(n)["op"] != "SETOP_NONE" {
		t.Fatal("Inner wrong")
	}
}

func TestHelpers(t *testing.T) {
	n := ASTNode{
		"sval": "hello",
		"ival": float64(42),
		"lst":  []any{1, 2, 3},
		"m":    ASTNode{"x": "y"},
	}
	if AsString(n, "sval") != "hello" {
		t.Error("AsString")
	}
	if AsInt(n, "ival") != 42 {
		t.Error("AsInt")
	}
	if len(AsList(n, "lst")) != 3 {
		t.Error("AsList")
	}
	if AsMap(n, "m")["x"] != "y" {
		t.Error("AsMap")
	}
	// absent keys
	if AsString(n, "zzz") != "" {
		t.Error("AsString missing -> empty")
	}
	if AsInt(n, "zzz") != 0 {
		t.Error("AsInt missing -> zero")
	}
}
