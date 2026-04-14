package jsrules

import (
	"github.com/dop251/goja"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// Rule is a JS-backed implementation of the rules.Rule contract, minus the
// import cycle that importing internal/rules would create. The cmd layer
// adapts this to analyzer.Runnable directly.
type Rule interface {
	ID() string
	Description() string
	DefaultSeverity() analyzer.Severity
	RequiresSchema() bool
	RequiresExplain() bool
	Check(ctx *analyzer.Context) []analyzer.Finding
}

type jsRule struct {
	engine   *Engine
	manifest Manifest
	check    goja.Callable
}

func (r *jsRule) ID() string                         { return r.manifest.ID }
func (r *jsRule) Description() string                { return r.manifest.Description }
func (r *jsRule) DefaultSeverity() analyzer.Severity { return r.manifest.severity() }
func (r *jsRule) RequiresSchema() bool               { return r.manifest.RequiresSchema }
func (r *jsRule) RequiresExplain() bool              { return r.manifest.RequiresExplain }

func (r *jsRule) Check(ctx *analyzer.Context) []analyzer.Finding {
	out, err := r.engine.invoke(r, ctx)
	if err != nil {
		// A runtime error in a user rule must not kill the whole run.
		// Surface it as an info-level finding so the author can see it.
		return []analyzer.Finding{{
			Rule:     r.manifest.ID,
			Severity: analyzer.SeverityInfo,
			Message:  "js rule error: " + err.Error(),
		}}
	}
	return out
}
