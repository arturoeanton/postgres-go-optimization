package report

import (
	"encoding/json"
	"io"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// JSON writes the findings as a JSON object with a stable schema:
//
//	{
//	  "findings": [ ...Finding objects... ],
//	  "summary": { "total": N, "error": N, "warn": N, "info": N }
//	}
func JSON(w io.Writer, findings analyzer.FindingList) error {
	counts := map[string]int{"error": 0, "warn": 0, "info": 0}
	for _, f := range findings {
		counts[f.Severity.String()]++
	}
	out := map[string]any{
		"findings": findings,
		"summary": map[string]any{
			"total": len(findings),
			"error": counts["error"],
			"warn":  counts["warn"],
			"info":  counts["info"],
		},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
