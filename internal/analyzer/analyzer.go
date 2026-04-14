package analyzer

// Runner executes a set of rules against a context and aggregates findings.
//
// The concrete rules live in internal/rules to avoid an import cycle.
// This package owns the primitive types (Context, Finding, Walker) and
// the orchestrator. Rules are injected via a Runnable.

import (
	"sort"
)

// Runnable is satisfied by anything that can check a Context.
// rules.Rule satisfies it, but we keep the concrete type in the rules
// package to decouple import order.
type Runnable interface {
	ID() string
	Check(*Context) []Finding
	RequiresSchema() bool
	RequiresExplain() bool
}

// Run executes each rule, skipping those whose requirements aren't met,
// and returns the merged, sorted findings.
func Run(ctx *Context, rules []Runnable) FindingList {
	var all FindingList
	for _, r := range rules {
		if r.RequiresSchema() && ctx.Schema == nil {
			continue
		}
		if r.RequiresExplain() && ctx.Explain == nil {
			continue
		}
		findings := r.Check(ctx)
		for i := range findings {
			if findings[i].Rule == "" {
				findings[i].Rule = r.ID()
			}
			if findings[i].Snippet == "" {
				findings[i].Snippet = ctx.Snippet(findings[i].Location)
			}
		}
		all = append(all, findings...)
	}
	sort.Sort(all)
	return all
}
