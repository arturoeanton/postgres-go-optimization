package jsrules

import (
	"github.com/dop251/goja"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// bindHelpers installs the `pg` global used by every JS rule. Each helper
// pushes the heavy work into Go so that rule authors only write predicate
// logic in JavaScript. The surface mirrors what internal/rules uses
// internally; see docs/js-rules.md for the catalog.
func bindHelpers(vm *goja.Runtime) {
	pg := vm.NewObject()

	// Tree traversal -------------------------------------------------------

	_ = pg.Set("walk", func(call goja.FunctionCall) goja.Value {
		tree := exportNode(call.Argument(0))
		if tree == nil {
			return goja.Undefined()
		}
		visitor, ok := goja.AssertFunction(call.Argument(1))
		if !ok {
			return goja.Undefined()
		}
		analyzer.Walk(tree, func(path []string, node analyzer.ASTNode) bool {
			res, err := visitor(goja.Undefined(), vm.ToValue(path), vm.ToValue(node))
			if err != nil {
				return false
			}
			// Visitor may return false to prune; otherwise we descend.
			if res == nil || goja.IsUndefined(res) || goja.IsNull(res) {
				return true
			}
			if b, ok := res.Export().(bool); ok {
				return b
			}
			return true
		})
		return goja.Undefined()
	})

	_ = pg.Set("nodeKind", func(call goja.FunctionCall) goja.Value {
		n := exportNode(call.Argument(0))
		return vm.ToValue(analyzer.NodeKind(n))
	})
	_ = pg.Set("inner", func(call goja.FunctionCall) goja.Value {
		n := exportNode(call.Argument(0))
		return vm.ToValue(analyzer.Inner(n))
	})
	_ = pg.Set("asString", func(call goja.FunctionCall) goja.Value {
		n := exportNode(call.Argument(0))
		k := call.Argument(1).String()
		return vm.ToValue(analyzer.AsString(n, k))
	})
	_ = pg.Set("asInt", func(call goja.FunctionCall) goja.Value {
		n := exportNode(call.Argument(0))
		k := call.Argument(1).String()
		return vm.ToValue(analyzer.AsInt(n, k))
	})
	_ = pg.Set("asList", func(call goja.FunctionCall) goja.Value {
		n := exportNode(call.Argument(0))
		k := call.Argument(1).String()
		return vm.ToValue(analyzer.AsList(n, k))
	})
	_ = pg.Set("asMap", func(call goja.FunctionCall) goja.Value {
		n := exportNode(call.Argument(0))
		k := call.Argument(1).String()
		return vm.ToValue(analyzer.AsMap(n, k))
	})

	// Convenience iterators ------------------------------------------------

	_ = pg.Set("forEachStmt", func(call goja.FunctionCall) goja.Value {
		tree := exportNode(call.Argument(0))
		visitor, ok := goja.AssertFunction(call.Argument(1))
		if !ok || tree == nil {
			return goja.Undefined()
		}
		for _, s := range analyzer.AsList(tree, "stmts") {
			sm, _ := s.(analyzer.ASTNode)
			stmt := analyzer.AsMap(sm, "stmt")
			kind := analyzer.NodeKind(stmt)
			if kind == "" {
				continue
			}
			_, _ = visitor(goja.Undefined(),
				vm.ToValue(kind),
				vm.ToValue(analyzer.Inner(stmt)),
				vm.ToValue(stmt),
			)
		}
		return goja.Undefined()
	})

	_ = pg.Set("forEachSelect", func(call goja.FunctionCall) goja.Value {
		tree := exportNode(call.Argument(0))
		visitor, ok := goja.AssertFunction(call.Argument(1))
		if !ok || tree == nil {
			return goja.Undefined()
		}
		analyzer.Walk(tree, func(_ []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) == "SelectStmt" {
				_, _ = visitor(goja.Undefined(), vm.ToValue(analyzer.Inner(n)))
			}
			return true
		})
		return goja.Undefined()
	})

	// findNodes returns every node of the requested kind anywhere in the
	// tree. Handy because a common rule shape is "scan for kind X, then
	// inspect each match in isolation".
	_ = pg.Set("findNodes", func(call goja.FunctionCall) goja.Value {
		tree := exportNode(call.Argument(0))
		kind := call.Argument(1).String()
		var out []analyzer.ASTNode
		if tree != nil {
			analyzer.Walk(tree, func(_ []string, n analyzer.ASTNode) bool {
				if analyzer.NodeKind(n) == kind {
					out = append(out, analyzer.Inner(n))
				}
				return true
			})
		}
		return vm.ToValue(out)
	})

	// firstLocation finds the first "location" offset in a subtree.
	_ = pg.Set("firstLocation", func(call goja.FunctionCall) goja.Value {
		n := exportNode(call.Argument(0))
		if n == nil {
			return vm.ToValue(0)
		}
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
		return vm.ToValue(loc)
	})

	// Column/table extraction ----------------------------------------------

	_ = pg.Set("columnRefs", func(call goja.FunctionCall) goja.Value {
		tree := exportNode(call.Argument(0))
		var out []string
		if tree == nil {
			return vm.ToValue(out)
		}
		analyzer.Walk(tree, func(_ []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "ColumnRef" {
				return true
			}
			fields := analyzer.AsList(analyzer.Inner(n), "fields")
			name := ""
			for _, f := range fields {
				fn, _ := f.(analyzer.ASTNode)
				if s := analyzer.AsString(analyzer.AsMap(fn, "String"), "sval"); s != "" {
					if name == "" {
						name = s
					} else {
						name = name + "." + s
					}
				}
			}
			if name != "" {
				out = append(out, name)
			}
			return true
		})
		return vm.ToValue(out)
	})

	_ = pg.Set("tableRefs", func(call goja.FunctionCall) goja.Value {
		tree := exportNode(call.Argument(0))
		var out []string
		if tree == nil {
			return vm.ToValue(out)
		}
		analyzer.Walk(tree, func(_ []string, n analyzer.ASTNode) bool {
			if analyzer.NodeKind(n) != "RangeVar" {
				return true
			}
			inner := analyzer.Inner(n)
			name := analyzer.AsString(inner, "relname")
			if schema := analyzer.AsString(inner, "schemaname"); schema != "" {
				name = schema + "." + name
			}
			if name != "" {
				out = append(out, name)
			}
			return true
		})
		return vm.ToValue(out)
	})

	// Finding builder ------------------------------------------------------

	// pg.finding(msg, suggestion?, locStart?, locEnd?) — convenience for
	// the common case where a rule returns a single-item array. Keeps rule
	// bodies tight; authors can always build the object literal by hand.
	_ = pg.Set("finding", func(call goja.FunctionCall) goja.Value {
		o := vm.NewObject()
		_ = o.Set("message", call.Argument(0).String())
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Argument(1)) {
			_ = o.Set("suggestion", call.Argument(1).String())
		}
		if len(call.Arguments) > 2 {
			loc := vm.NewObject()
			_ = loc.Set("start", call.Argument(2).ToInteger())
			if len(call.Arguments) > 3 {
				_ = loc.Set("end", call.Argument(3).ToInteger())
			} else {
				_ = loc.Set("end", call.Argument(2).ToInteger()+1)
			}
			_ = o.Set("location", loc)
		}
		return o
	})

	_ = vm.Set("pg", pg)
}

// exportNode unwraps a goja.Value back into an analyzer.ASTNode, handling
// both the usual map case and the (rare) nil/undefined case gracefully.
func exportNode(v goja.Value) analyzer.ASTNode {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	exp := v.Export()
	if m, ok := exp.(map[string]any); ok {
		return analyzer.ASTNode(m)
	}
	if m, ok := exp.(analyzer.ASTNode); ok {
		return m
	}
	return nil
}
