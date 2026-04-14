// Package report formats a FindingList for human and machine consumption.
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// TextOptions controls rendering of the text report.
type TextOptions struct {
	Color   bool
	Verbose bool   // include Explanation + Evidence
	Source  string // the original SQL (used to show line/col)
}

// Text writes a human-readable report to w.
func Text(w io.Writer, findings analyzer.FindingList, opts TextOptions) {
	if len(findings) == 0 {
		fmt.Fprintln(w, ok(opts.Color, "✓ no findings — query looks clean"))
		return
	}
	for i, f := range findings {
		line, col := lineCol(opts.Source, f.Location.Start)
		sev := severityLabel(f.Severity, opts.Color)
		fmt.Fprintf(w, "%s  %s  [%s]  at %d:%d\n",
			sev, f.Message, f.Rule, line, col)
		if f.Snippet != "" {
			fmt.Fprintf(w, "    ╰─ %s\n", truncate(f.Snippet, 120))
		}
		if opts.Verbose && f.Explanation != "" {
			fmt.Fprintf(w, "    why: %s\n", wrap(f.Explanation, 100, "         "))
		}
		if f.Suggestion != "" {
			fmt.Fprintf(w, "    fix: %s\n", wrap(f.Suggestion, 100, "         "))
		}
		if opts.Verbose && f.Evidence != "" {
			fmt.Fprintf(w, "    ref: %s\n", f.Evidence)
		}
		if i < len(findings)-1 {
			fmt.Fprintln(w)
		}
	}

	// Summary
	counts := map[analyzer.Severity]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}
	fmt.Fprintf(w, "\n%d finding(s): %d error, %d warn, %d info\n",
		len(findings), counts[analyzer.SeverityError],
		counts[analyzer.SeverityWarn], counts[analyzer.SeverityInfo])
}

func severityLabel(s analyzer.Severity, color bool) string {
	label := "[" + strings.ToUpper(s.String()) + "]"
	if !color {
		return label
	}
	switch s {
	case analyzer.SeverityError:
		return "\x1b[31;1m" + label + "\x1b[0m"
	case analyzer.SeverityWarn:
		return "\x1b[33;1m" + label + "\x1b[0m"
	case analyzer.SeverityInfo:
		return "\x1b[36m" + label + "\x1b[0m"
	}
	return label
}

func ok(color bool, s string) string {
	if !color {
		return s
	}
	return "\x1b[32;1m" + s + "\x1b[0m"
}

func lineCol(src string, off int) (int, int) {
	line, col := 1, 1
	if off < 0 || off > len(src) {
		return line, col
	}
	for i := 0; i < off; i++ {
		if src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func wrap(s string, width int, cont string) string {
	words := strings.Fields(s)
	var lines []string
	line := ""
	for _, w := range words {
		if len(line)+1+len(w) > width && line != "" {
			lines = append(lines, line)
			line = w
		} else if line == "" {
			line = w
		} else {
			line += " " + w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"+cont)
}
