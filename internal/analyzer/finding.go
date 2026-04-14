// Package analyzer orchestrates the analysis of a SQL query.
//
// A Finding describes a single issue detected by a Rule. Multiple Rules
// can produce multiple Findings per query. Findings carry a Severity, a
// human-readable Message, an Explanation that cites PostgreSQL internals,
// and optionally a Suggestion with a concrete rewrite.
package analyzer

import "fmt"

// Severity classifies the importance of a finding.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarn
	SeverityError
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarn:
		return "warn"
	case SeverityError:
		return "error"
	}
	return "unknown"
}

// ParseSeverity is the reverse of String.
func ParseSeverity(s string) (Severity, error) {
	switch s {
	case "info":
		return SeverityInfo, nil
	case "warn", "warning":
		return SeverityWarn, nil
	case "error":
		return SeverityError, nil
	}
	return 0, fmt.Errorf("unknown severity %q", s)
}

// Range is a character span in the source query (0-indexed byte offsets).
type Range struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Finding is a single issue reported by a Rule.
type Finding struct {
	Rule        string   `json:"rule"`
	Severity    Severity `json:"severity"`
	Message     string   `json:"message"`
	Explanation string   `json:"explanation,omitempty"`
	Suggestion  string   `json:"suggestion,omitempty"`
	Evidence    string   `json:"evidence,omitempty"` // reference to PG source or guide
	Location    Range    `json:"location,omitempty"`
	Snippet     string   `json:"snippet,omitempty"` // excerpt of the source at Location
}

// FindingList is a sortable, printable list of findings.
type FindingList []Finding

func (f FindingList) Len() int      { return len(f) }
func (f FindingList) Swap(i, j int) { f[i], f[j] = f[j], f[i] }
func (f FindingList) Less(i, j int) bool {
	if f[i].Severity != f[j].Severity {
		return f[i].Severity > f[j].Severity // higher severity first
	}
	if f[i].Location.Start != f[j].Location.Start {
		return f[i].Location.Start < f[j].Location.Start
	}
	return f[i].Rule < f[j].Rule
}

// MaxSeverity returns the highest severity among the findings.
func (f FindingList) MaxSeverity() Severity {
	m := SeverityInfo
	for _, x := range f {
		if x.Severity > m {
			m = x.Severity
		}
	}
	return m
}
