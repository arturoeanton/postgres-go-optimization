// Package rules contains the optimization/linting rules.
//
// Each Rule implements the Rule interface and registers itself in the global
// Registry at package init time. The analyzer runs all enabled rules over a
// single Context and aggregates Findings.
//
// Rules are stateless: they receive the full Context and return findings.
// A rule may require a live schema (--db) or an EXPLAIN plan (--explain);
// the analyzer skips rules whose requirements are not met.
package rules

import (
	"sort"
	"sync"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// Rule is the interface every check implements.
type Rule interface {
	ID() string
	Description() string
	DefaultSeverity() analyzer.Severity
	RequiresSchema() bool
	RequiresExplain() bool
	Check(ctx *analyzer.Context) []analyzer.Finding
}

var (
	mu       sync.Mutex
	registry = map[string]Rule{}
)

// Register adds a rule. Panics on duplicate IDs (catches programming bugs).
func Register(r Rule) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[r.ID()]; dup {
		panic("rule registered twice: " + r.ID())
	}
	registry[r.ID()] = r
}

// All returns every registered rule, sorted by ID for stable ordering.
func All() []Rule {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Rule, 0, len(registry))
	for _, r := range registry {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Get fetches a rule by ID. Returns (nil, false) if not found.
func Get(id string) (Rule, bool) {
	mu.Lock()
	defer mu.Unlock()
	r, ok := registry[id]
	return r, ok
}

// Select filters All() according to a spec like "all", "r1,r2", "all,-r3".
// Returns the resulting rule set.
func Select(spec string) ([]Rule, error) {
	if spec == "" || spec == "all" {
		return All(), nil
	}
	// Parse comma-separated tokens.
	parts := splitTrim(spec, ",")
	enabled := map[string]bool{}
	start := false
	if parts[0] == "all" {
		start = true
		for _, r := range All() {
			enabled[r.ID()] = true
		}
		parts = parts[1:]
	}
	for _, p := range parts {
		if p == "" {
			continue
		}
		if p[0] == '-' {
			delete(enabled, p[1:])
			continue
		}
		if _, ok := Get(p); !ok {
			return nil, &unknownRuleError{ID: p}
		}
		enabled[p] = true
	}
	_ = start
	// Build result.
	out := make([]Rule, 0, len(enabled))
	for _, r := range All() {
		if enabled[r.ID()] {
			out = append(out, r)
		}
	}
	return out, nil
}

type unknownRuleError struct{ ID string }

func (e *unknownRuleError) Error() string { return "unknown rule: " + e.ID }

func splitTrim(s, sep string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		c := string(r)
		if c == sep {
			out = append(out, trimSpace(cur))
			cur = ""
		} else {
			cur += c
		}
	}
	out = append(out, trimSpace(cur))
	return out
}

func trimSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	j := len(s)
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}
