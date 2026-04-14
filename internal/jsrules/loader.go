package jsrules

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LoadDir discovers every rule in the given directory. A rule is a
// subdirectory that contains both manifest.json and the entry JS file
// referenced by the manifest (main.js by default). Directories missing
// either file are skipped silently so the tree can hold READMEs, drafts,
// or user scratch space without breaking the loader.
//
// The returned Engine owns the loaded rules; pass Engine.Rules() to the
// CLI wiring that builds analyzer.Runnable slices.
func LoadDir(root string) (*Engine, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("js rules dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("js rules dir: %s is not a directory", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("js rules dir: %w", err)
	}
	// Stable order by directory name so listings and tests are deterministic.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	engine := NewEngine()
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(root, ent.Name())
		manifestPath := filepath.Join(dir, "manifest.json")
		if _, err := os.Stat(manifestPath); err != nil {
			continue
		}
		m, err := loadManifest(manifestPath)
		if err != nil {
			return nil, err
		}
		srcPath := filepath.Join(dir, m.Entry)
		src, err := os.ReadFile(srcPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.ID, err)
		}
		if err := engine.addRule(dir, m, string(src)); err != nil {
			return nil, err
		}
	}
	return engine, nil
}
