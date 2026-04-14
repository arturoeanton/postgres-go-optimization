package jsrules

import (
	"fmt"
	"sync"

	"github.com/dop251/goja"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// Engine owns a goja runtime plus the set of JS rules loaded against it.
//
// The goja runtime is not safe for concurrent use, so every entry point
// takes the engine-level mutex. In practice pgopt analyzes one query at a
// time, so contention is not a concern; the mutex is defensive for callers
// that might share an Engine across goroutines later.
//
// Heavy AST work is implemented in Go and exposed to JS via the global
// `pg` object. Rules are expected to call pg.walk / pg.forEachSelect /
// pg.forEachStmt rather than traversing the tree themselves, which keeps
// interpretation overhead bounded to the visitor callbacks.
type Engine struct {
	mu    sync.Mutex
	vm    *goja.Runtime
	rules []*jsRule
}

// NewEngine builds an empty engine with the standard `pg` helpers bound.
// Use Load or LoadDir to populate it with rules.
func NewEngine() *Engine {
	e := &Engine{vm: goja.New()}
	e.vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	bindHelpers(e.vm)
	return e
}

// Rules returns the loaded rules as analyzer.Runnable-compatible Rule
// implementations. The returned slice is safe to iterate; the underlying
// rule objects share the Engine, so calls must not overlap.
func (e *Engine) Rules() []Rule {
	out := make([]Rule, 0, len(e.rules))
	for _, r := range e.rules {
		out = append(out, r)
	}
	return out
}

// addRule compiles a single rule against this engine's runtime.
func (e *Engine) addRule(dir string, m Manifest, source string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Wrap the user code so we can grab the exported `check` via a
	// per-rule IIFE scope, avoiding collisions across rules in the same
	// runtime. We deliberately do not use CommonJS or ES modules — goja
	// supports neither out of the box, and the wrapper keeps the mental
	// model minimal: "write a function called check".
	wrapped := fmt.Sprintf(
		"(function(){\n%s\n;return (typeof check === 'function') ? check : null;})()",
		source,
	)
	prog, err := goja.Compile(dir+"/"+m.Entry, wrapped, true)
	if err != nil {
		return fmt.Errorf("%s: compile: %w", m.ID, err)
	}
	val, err := e.vm.RunProgram(prog)
	if err != nil {
		return fmt.Errorf("%s: load: %w", m.ID, err)
	}
	fn, ok := goja.AssertFunction(val)
	if !ok {
		return fmt.Errorf("%s: main.js must define a top-level function check(ctx)", m.ID)
	}
	for _, existing := range e.rules {
		if existing.manifest.ID == m.ID {
			return fmt.Errorf("%s: duplicate rule id", m.ID)
		}
	}
	e.rules = append(e.rules, &jsRule{
		engine:   e,
		manifest: m,
		check:    fn,
	})
	return nil
}

// invoke is the hot path: called once per (rule, query). Builds the ctx
// object, calls check(ctx), and coerces the returned array back to Go.
func (e *Engine) invoke(r *jsRule, ctx *analyzer.Context) ([]analyzer.Finding, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	jsCtx := e.vm.NewObject()
	_ = jsCtx.Set("ast", e.vm.ToValue(ctx.AST))
	_ = jsCtx.Set("source", ctx.Source)
	if ctx.Schema != nil {
		_ = jsCtx.Set("schema", e.vm.ToValue(ctx.Schema))
	} else {
		_ = jsCtx.Set("schema", goja.Null())
	}
	if ctx.Explain != nil {
		_ = jsCtx.Set("explain", e.vm.ToValue(ctx.Explain))
	} else {
		_ = jsCtx.Set("explain", goja.Null())
	}

	result, err := r.check(goja.Undefined(), jsCtx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.manifest.ID, err)
	}
	return coerceFindings(result, r.manifest), nil
}

// coerceFindings converts the JS return value into a slice of Findings.
// The JS side returns either nothing (undefined/null), a single object,
// or an array. We accept all three for ergonomics.
func coerceFindings(v goja.Value, m Manifest) []analyzer.Finding {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	exp := v.Export()
	switch raw := exp.(type) {
	case []any:
		out := make([]analyzer.Finding, 0, len(raw))
		for _, item := range raw {
			if f, ok := toFinding(item, m); ok {
				out = append(out, f)
			}
		}
		return out
	case map[string]any:
		if f, ok := toFinding(raw, m); ok {
			return []analyzer.Finding{f}
		}
	}
	return nil
}

func toFinding(item any, m Manifest) (analyzer.Finding, bool) {
	obj, ok := item.(map[string]any)
	if !ok {
		return analyzer.Finding{}, false
	}
	f := analyzer.Finding{
		Rule:     m.ID,
		Severity: m.severity(),
		Evidence: m.Evidence,
	}
	if s, ok := obj["message"].(string); ok {
		f.Message = s
	}
	if s, ok := obj["explanation"].(string); ok {
		f.Explanation = s
	}
	if s, ok := obj["suggestion"].(string); ok {
		f.Suggestion = s
	}
	if s, ok := obj["evidence"].(string); ok && s != "" {
		f.Evidence = s
	}
	if sev, ok := obj["severity"].(string); ok && sev != "" {
		if parsed, err := analyzer.ParseSeverity(sev); err == nil {
			f.Severity = parsed
		}
	}
	if loc, ok := obj["location"].(map[string]any); ok {
		f.Location.Start = intOf(loc["start"])
		f.Location.End = intOf(loc["end"])
		if f.Location.End <= f.Location.Start {
			f.Location.End = f.Location.Start + 1
		}
	}
	if f.Message == "" {
		return analyzer.Finding{}, false
	}
	return f, true
}

func intOf(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}
