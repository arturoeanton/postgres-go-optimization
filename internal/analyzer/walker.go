package analyzer

import (
	"encoding/json"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// ASTNode is a node in the JSON-AST tree returned by pg_query.ParseToJSON.
// We walk the AST as generic maps/slices for flexibility across rules.
type ASTNode = map[string]any

// ParseJSON parses a SQL query and returns a generic JSON tree. It is the
// common AST representation used by rules. Location fields ("location" keys)
// are byte offsets into the source, as the PostgreSQL parser emits.
func ParseJSON(sql string) (ASTNode, error) {
	js, err := pg_query.ParseToJSON(sql)
	if err != nil {
		return nil, err
	}
	var root ASTNode
	if err := json.Unmarshal([]byte(js), &root); err != nil {
		return nil, err
	}
	return root, nil
}

// Walk traverses the AST depth-first. For each map (object) encountered,
// the visitor is called with the current path (keys from root) and the node
// itself. If the visitor returns false, the subtree is not descended further.
func Walk(tree any, visitor func(path []string, node ASTNode) bool) {
	walk(nil, tree, visitor)
}

func walk(path []string, v any, visitor func([]string, ASTNode) bool) {
	switch t := v.(type) {
	case ASTNode:
		if !visitor(path, t) {
			return
		}
		for k, child := range t {
			walk(append(path, k), child, visitor)
		}
	case []any:
		for i, child := range t {
			_ = i
			walk(path, child, visitor)
		}
	}
}

// NodeKind returns the single key of a wrapped node like {"SelectStmt": {...}}.
// Many pg_query nodes have exactly one meaningful wrapper key. Returns "" if
// the node has zero or multiple top-level keys.
func NodeKind(n ASTNode) string {
	if len(n) != 1 {
		return ""
	}
	for k := range n {
		return k
	}
	return ""
}

// Inner returns the inner map of a wrapped node. Returns nil if the node is
// not a single-keyed wrapper over a map.
func Inner(n ASTNode) ASTNode {
	if len(n) != 1 {
		return nil
	}
	for _, v := range n {
		if m, ok := v.(ASTNode); ok {
			return m
		}
	}
	return nil
}

// AsString returns the string at key or "" if missing/wrong type.
func AsString(n ASTNode, key string) string {
	if n == nil {
		return ""
	}
	v, ok := n[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// AsInt returns the int at key or 0 if missing/wrong type.
func AsInt(n ASTNode, key string) int {
	if n == nil {
		return 0
	}
	v, ok := n[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

// AsList returns the []any at key or nil.
func AsList(n ASTNode, key string) []any {
	if n == nil {
		return nil
	}
	v, ok := n[key]
	if !ok {
		return nil
	}
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// AsMap returns the map at key or nil.
func AsMap(n ASTNode, key string) ASTNode {
	if n == nil {
		return nil
	}
	v, ok := n[key]
	if !ok {
		return nil
	}
	if m, ok := v.(ASTNode); ok {
		return m
	}
	return nil
}
