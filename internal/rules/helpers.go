package rules

import "github.com/arturoeanton/postgres-go-optimization/internal/analyzer"

// forEachStmt invokes visit(kind, innerMap) for every top-level statement
// in the AST. Example kinds: "SelectStmt", "UpdateStmt", "DeleteStmt",
// "InsertStmt", "TruncateStmt", "TransactionStmt".
func forEachStmt(ast analyzer.ASTNode, visit func(kind string, inner analyzer.ASTNode, wrapper analyzer.ASTNode)) {
	stmts := analyzer.AsList(ast, "stmts")
	for _, s := range stmts {
		sm, _ := s.(analyzer.ASTNode)
		stmt := analyzer.AsMap(sm, "stmt")
		kind := analyzer.NodeKind(stmt)
		if kind == "" {
			continue
		}
		visit(kind, analyzer.Inner(stmt), stmt)
	}
}

// forEachSelect invokes visit(inner) for every SelectStmt anywhere in the AST,
// including nested SubLinks and set-operations.
func forEachSelect(ast analyzer.ASTNode, visit func(sel analyzer.ASTNode)) {
	analyzer.Walk(ast, func(_ []string, n analyzer.ASTNode) bool {
		if analyzer.NodeKind(n) == "SelectStmt" {
			visit(analyzer.Inner(n))
		}
		return true
	})
}

// firstLocation finds any "location" scalar in a subtree and returns it.
func firstLocation(n analyzer.ASTNode) int {
	var loc int
	var found bool
	analyzer.Walk(n, func(_ []string, x analyzer.ASTNode) bool {
		if found {
			return false
		}
		if v, ok := x["location"]; ok {
			if f, ok := v.(float64); ok {
				loc = int(f)
				found = true
				return false
			}
		}
		return true
	})
	return loc
}

// exprIsColumnRef reports whether expr is a bare ColumnRef (maybe qualified).
func exprIsColumnRef(expr analyzer.ASTNode) bool {
	return analyzer.NodeKind(expr) == "ColumnRef"
}
