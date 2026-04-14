package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_StdinSelectStar(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"-"}, strings.NewReader("SELECT * FROM t"), &out, &errb)
	if code != exitFinds {
		t.Errorf("exit code: got %d want %d (stderr=%s)", code, exitFinds, errb.String())
	}
	if !strings.Contains(out.String(), "select_star") {
		t.Errorf("stdout missing select_star; got:\n%s", out.String())
	}
}

func TestCLI_JSONFormat(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--format", "json", "-"}, strings.NewReader("SELECT * FROM t"), &out, &errb)
	if code != exitFinds {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if v["findings"] == nil {
		t.Error("json missing findings")
	}
}

func TestCLI_CleanQuery(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"-"}, strings.NewReader("SELECT id, name FROM t WHERE id = 1"), &out, &errb)
	if code != exitOK {
		t.Errorf("expected exit 0 for clean query, got %d (stderr=%s)", code, errb.String())
	}
}

func TestCLI_ParseError(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"-"}, strings.NewReader("SELEKT 1 FROM"), &out, &errb)
	if code != exitParse {
		t.Errorf("want exitParse (%d), got %d", exitParse, code)
	}
}

func TestCLI_ListRules(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--list-rules"}, strings.NewReader(""), &out, &errb)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"select_star", "offset_pagination", "missing_where"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list-rules missing %q", want)
		}
	}
}

func TestCLI_RuleFilter(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--rules", "offset_pagination", "-"},
		strings.NewReader("SELECT * FROM t LIMIT 10 OFFSET 100000"), &out, &errb)
	if code != exitFinds {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if strings.Contains(out.String(), "select_star") {
		t.Error("expected select_star to be filtered out")
	}
	if !strings.Contains(out.String(), "offset_pagination") {
		t.Error("expected offset_pagination")
	}
}

func TestCLI_MinSeverity(t *testing.T) {
	var out, errb bytes.Buffer
	// raise min-severity to error so only missing_where-style errors show
	code := run([]string{"--min-severity", "error", "--fail-on", "error", "-"},
		strings.NewReader("SELECT * FROM t"), &out, &errb)
	if code != exitOK {
		t.Errorf("expected exit 0 (warnings filtered out), got %d", code)
	}
}

func TestCLI_FailOn(t *testing.T) {
	var out, errb bytes.Buffer
	// With --fail-on error and a warn finding, exit should be 0
	code := run([]string{"--fail-on", "error", "-"},
		strings.NewReader("SELECT * FROM t"), &out, &errb)
	if code != exitOK {
		t.Errorf("got %d want %d (should not fail on warn when --fail-on=error)", code, exitOK)
	}
}

func TestCLI_IgnoreFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".pgoptignore")
	if err := os.WriteFile(path, []byte("# silence star for this project\nselect_star\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"--ignore-file", path, "-"},
		strings.NewReader("SELECT * FROM t"), &out, &errb)
	if code != exitOK {
		t.Fatalf("exit %d (expected clean after ignore): stderr=%s stdout=%s", code, errb.String(), out.String())
	}
	if strings.Contains(out.String(), "select_star") {
		t.Errorf("select_star leaked through ignore file:\n%s", out.String())
	}
}

func TestCLI_NoIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".pgoptignore")
	if err := os.WriteFile(path, []byte("select_star\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	// --no-ignore-file wins even if --ignore-file is passed.
	code := run([]string{"--ignore-file", path, "--no-ignore-file", "-"},
		strings.NewReader("SELECT * FROM t"), &out, &errb)
	if code != exitFinds {
		t.Fatalf("exit %d: stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "select_star") {
		t.Errorf("expected select_star after --no-ignore-file; got:\n%s", out.String())
	}
}

func TestCLI_IgnoreFileMissingExplicit(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--ignore-file", "/nonexistent/.pgoptignore", "-"},
		strings.NewReader("SELECT 1"), &out, &errb)
	if code != exitConfig {
		t.Errorf("explicit missing ignore file should be a config error, got %d", code)
	}
}

func TestCLI_JSRules(t *testing.T) {
	dir := t.TempDir()
	ruleDir := filepath.Join(dir, "js_hello")
	if err := os.MkdirAll(ruleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"js_hello","description":"d","severity":"warn","requiresSchema":false,"requiresExplain":false}`
	_ = os.WriteFile(filepath.Join(ruleDir, "manifest.json"), []byte(manifest), 0o644)
	_ = os.WriteFile(filepath.Join(ruleDir, "main.js"), []byte(
		`function check(ctx) { return [pg.finding("js fired", "do x", 0, 1)]; }`,
	), 0o644)

	var out, errb bytes.Buffer
	code := run([]string{"--js-rules", "--js-rules-dir", dir, "--no-ignore-file", "-"},
		strings.NewReader("SELECT 1"), &out, &errb)
	if code != exitFinds {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "js_hello") {
		t.Errorf("expected js rule in output:\n%s", out.String())
	}
}

func TestCLI_ListRulesIncludesJS(t *testing.T) {
	dir := t.TempDir()
	ruleDir := filepath.Join(dir, "js_listed")
	_ = os.MkdirAll(ruleDir, 0o755)
	_ = os.WriteFile(filepath.Join(ruleDir, "manifest.json"),
		[]byte(`{"id":"js_listed","description":"desc","severity":"info","requiresSchema":false,"requiresExplain":false}`), 0o644)
	_ = os.WriteFile(filepath.Join(ruleDir, "main.js"), []byte(`function check(){return [];}`), 0o644)

	var out, errb bytes.Buffer
	code := run([]string{"--js-rules", "--js-rules-dir", dir, "--list-rules"},
		strings.NewReader(""), &out, &errb)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "js_listed") || !strings.Contains(out.String(), "[js]") {
		t.Errorf("--list-rules should show JS rule with [js] tag:\n%s", out.String())
	}
}

func TestCLI_JSRulesDirMissing(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--js-rules", "--js-rules-dir", "/nope/nohow", "-"},
		strings.NewReader("SELECT 1"), &out, &errb)
	if code != exitConfig {
		t.Errorf("missing JS rules dir should be a config error, got %d", code)
	}
}

func TestCLI_UnknownRule(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--rules", "not_a_real_rule", "-"},
		strings.NewReader("SELECT 1"), &out, &errb)
	if code != exitConfig {
		t.Errorf("unknown rule should be a config error, got %d", code)
	}
}

func TestCLI_FileInput(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "q*.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("SELECT * FROM t")
	_ = f.Close()

	var out, errb bytes.Buffer
	code := run([]string{f.Name()}, strings.NewReader(""), &out, &errb)
	if code != exitFinds {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "select_star") {
		t.Errorf("file input: want select_star in output:\n%s", out.String())
	}
}

func TestCLI_FileNotFound(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"/no/such/file.sql"}, strings.NewReader(""), &out, &errb)
	if code != exitConfig {
		t.Errorf("missing file should be config error, got %d", code)
	}
}

func TestCLI_EmptyInput(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"-"}, strings.NewReader("   \n\t"), &out, &errb)
	if code != exitConfig {
		t.Errorf("empty input should be config error, got %d", code)
	}
}

func TestCLI_BadSeverityFlags(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--min-severity", "nonsense", "-"},
		strings.NewReader("SELECT 1"), &out, &errb)
	if code != exitConfig {
		t.Errorf("bad --min-severity should be config error, got %d", code)
	}

	errb.Reset()
	out.Reset()
	code = run([]string{"--fail-on", "nonsense", "-"},
		strings.NewReader("SELECT 1"), &out, &errb)
	if code != exitConfig {
		t.Errorf("bad --fail-on should be config error, got %d", code)
	}
}

func TestCLI_BadFormat(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--format", "xml", "-"},
		strings.NewReader("SELECT * FROM t"), &out, &errb)
	if code != exitConfig {
		t.Errorf("unknown format should be config error, got %d", code)
	}
}

func TestCLI_QuietSuppressesOutput(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--quiet", "-"},
		strings.NewReader("SELECT * FROM t"), &out, &errb)
	if code != exitFinds {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("--quiet should suppress stdout, got %d bytes", out.Len())
	}
}

func TestCLI_TooManyArgs(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"a.sql", "b.sql"}, strings.NewReader(""), &out, &errb)
	if code != exitConfig {
		t.Errorf("more than one positional arg should be config error, got %d", code)
	}
}

func TestCLI_Version(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--version"}, strings.NewReader(""), &out, &errb)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "pgopt") {
		t.Errorf("--version output should include 'pgopt', got %q", out.String())
	}
}

func TestResolveColor(t *testing.T) {
	if !resolveColor("always", &bytes.Buffer{}) {
		t.Error("--color=always should force true")
	}
	if resolveColor("never", &bytes.Buffer{}) {
		t.Error("--color=never should force false")
	}
	// bytes.Buffer is not *os.File, so auto should return false.
	if resolveColor("auto", &bytes.Buffer{}) {
		t.Error("--color=auto on non-tty should be false")
	}
}
