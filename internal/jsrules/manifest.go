package jsrules

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// Manifest is the on-disk metadata that sits next to a rule's main.js.
// It deliberately mirrors the Go Rule interface so that authoring a JS rule
// feels like authoring a Go one.
type Manifest struct {
	ID              string `json:"id"`
	Description     string `json:"description"`
	Severity        string `json:"severity"`        // "info" | "warn" | "error"
	RequiresSchema  bool   `json:"requiresSchema"`  //nolint:revive // user-facing JSON key
	RequiresExplain bool   `json:"requiresExplain"` //nolint:revive // user-facing JSON key
	Evidence        string `json:"evidence,omitempty"`
	// Entry is the filename of the JS source relative to the manifest.
	// Defaults to "main.js" if empty.
	Entry string `json:"entry,omitempty"`
}

// loadManifest reads and validates a manifest.json file.
func loadManifest(path string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	if err := m.validate(); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	if m.Entry == "" {
		m.Entry = "main.js"
	}
	return m, nil
}

func (m Manifest) validate() error {
	if m.ID == "" {
		return errors.New("manifest: id is required")
	}
	if m.Description == "" {
		return errors.New("manifest: description is required")
	}
	if _, err := analyzer.ParseSeverity(m.Severity); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	return nil
}

func (m Manifest) severity() analyzer.Severity {
	s, _ := analyzer.ParseSeverity(m.Severity)
	return s
}
