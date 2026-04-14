// Package rewriter applies safe, deterministic text rewrites to the
// original SQL based on findings produced by rules.
//
// Our rewriter operates on the source TEXT (not on the AST), using the
// byte offsets that pg_query exposes on nearly every AST node. This
// preserves the user's formatting, comments, and layout.
//
// Philosophy: we only apply rewrites that are algebraically equivalent
// and cannot change semantics. If a rule is unsure, it emits a Finding
// with a Suggestion but no RewritePatch — the user applies the fix.
package rewriter

import (
	"sort"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// Patch describes a single text replacement: replace source[Start:End]
// with Replacement. Patches can be merged safely only when they do not
// overlap; overlapping patches lose later ones (a warning is emitted).
type Patch struct {
	Start       int
	End         int
	Replacement string
	Rule        string
}

// Apply applies a set of patches to source text and returns the result.
// Overlapping patches are skipped with a warning message appended.
func Apply(source string, patches []Patch) (string, []string) {
	if len(patches) == 0 {
		return source, nil
	}
	// Sort by Start, descending — so that applying them doesn't shift
	// offsets of later patches.
	sorted := make([]Patch, len(patches))
	copy(sorted, patches)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start > sorted[j].Start })

	var warnings []string
	out := source
	var lastStart int = len(source) + 1
	for _, p := range sorted {
		if p.End > lastStart {
			warnings = append(warnings, "skipping overlapping patch from rule "+p.Rule)
			continue
		}
		if p.Start < 0 || p.End > len(out) || p.Start >= p.End {
			warnings = append(warnings, "skipping invalid patch from rule "+p.Rule)
			continue
		}
		out = out[:p.Start] + p.Replacement + out[p.End:]
		lastStart = p.Start
	}
	return out, warnings
}

// PatchesFromFindings extracts any patches encoded as rewriter hints in the
// findings' Suggestion field. We keep this conservative: the current public
// rules do NOT emit patches directly — they produce suggestions as prose.
// This function is a placeholder that today returns nothing; it exists so
// that future rules can enable --fix on specific safe rewrites.
func PatchesFromFindings([]analyzer.Finding) []Patch { return nil }
