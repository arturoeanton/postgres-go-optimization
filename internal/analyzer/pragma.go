package analyzer

import (
	"strings"
)

// Pragma markers recognised inside SQL comments. Two shapes:
//
//	-- pgopt:ignore=rule_id[,rule_id...]   — silence every subsequent
//	                                         finding for those rule IDs
//	                                         in the current source file.
//	-- pgopt:ignore-next                   — silence every finding
//	                                         whose Location falls on the
//	                                         next non-comment line.
//
// Pragmas are intentionally file-scoped: they cannot leak across files
// because each pgopt invocation processes a single input. They also
// cannot silence parse errors — the pragma is part of the source that
// must already parse.
//
// ParsePragmas is pure; callers pass the result into FilterFindings.
type Pragmas struct {
	// ignored rule IDs that apply to the entire file.
	File map[string]bool
	// ignore-next applies to findings whose Location byte falls within
	// the byte range [start, end) of the targeted line. Stored as a
	// sorted slice of ranges; overlaps are permitted and harmless.
	IgnoreNext []ignoreRange
}

type ignoreRange struct {
	start, end int
}

// ParsePragmas scans src for pgopt pragma comments and returns the set
// of rules to drop. The scan is linear and forgiving: a malformed
// pragma is ignored rather than elevated to a parse error.
func ParsePragmas(src string) Pragmas {
	p := Pragmas{File: map[string]bool{}}

	lines := splitLinesWithOffsets(src)
	for i, ln := range lines {
		trim := strings.TrimSpace(ln.text)
		if !strings.HasPrefix(trim, "--") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(trim, "--"))
		if !strings.HasPrefix(body, "pgopt:") {
			continue
		}
		body = strings.TrimPrefix(body, "pgopt:")

		switch {
		case strings.HasPrefix(body, "ignore-next"):
			// Target the nearest following non-comment, non-blank line.
			for j := i + 1; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j].text)
				if t == "" || strings.HasPrefix(t, "--") {
					continue
				}
				p.IgnoreNext = append(p.IgnoreNext,
					ignoreRange{start: lines[j].start, end: lines[j].end})
				break
			}

		case strings.HasPrefix(body, "ignore="):
			list := strings.TrimPrefix(body, "ignore=")
			for _, id := range strings.Split(list, ",") {
				id = strings.TrimSpace(id)
				if id != "" {
					p.File[id] = true
				}
			}
		}
	}
	return p
}

// FilterFindings drops any finding silenced by the parsed pragmas.
// The order of findings is preserved.
func FilterFindings(fs FindingList, p Pragmas) FindingList {
	if len(p.File) == 0 && len(p.IgnoreNext) == 0 {
		return fs
	}
	out := fs[:0]
	for _, f := range fs {
		if p.File[f.Rule] {
			continue
		}
		if coveredByIgnoreNext(f.Location.Start, p.IgnoreNext) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func coveredByIgnoreNext(off int, ranges []ignoreRange) bool {
	for _, r := range ranges {
		if off >= r.start && off < r.end {
			return true
		}
	}
	return false
}

type lineSpan struct {
	text       string
	start, end int // byte offsets [start, end) in source
}

func splitLinesWithOffsets(src string) []lineSpan {
	var out []lineSpan
	start := 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			out = append(out, lineSpan{text: src[start:i], start: start, end: i})
			start = i + 1
		}
	}
	if start <= len(src) {
		out = append(out, lineSpan{text: src[start:], start: start, end: len(src)})
	}
	return out
}
