// Command pgopt analyzes and optimizes SQL queries for PostgreSQL.
//
// It is a pipe-friendly CLI that uses the real PostgreSQL parser via
// pg_query_go to parse the query into an AST, then applies a set of
// rules that encode hard-won knowledge about PostgreSQL internals. Many
// rules work offline; others (schema-aware, EXPLAIN-based) optionally
// connect to a live database.
//
// Exit codes:
//
//	0 — no findings at or above --fail-on severity (default warn)
//	1 — findings present at or above --fail-on severity
//	2 — SQL could not be parsed
//	3 — configuration or I/O error
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
	"github.com/arturoeanton/postgres-go-optimization/internal/explain"
	"github.com/arturoeanton/postgres-go-optimization/internal/jsrules"
	"github.com/arturoeanton/postgres-go-optimization/internal/report"
	"github.com/arturoeanton/postgres-go-optimization/internal/rules"
	"github.com/arturoeanton/postgres-go-optimization/internal/schema"
)

const (
	exitOK     = 0
	exitFinds  = 1
	exitParse  = 2
	exitConfig = 3
)

// Version is populated at build time via -ldflags. Example:
//
//	go build -ldflags="-X main.Version=0.1.0-alpha" ./cmd/pgopt
//
// Defaults to "dev" for developer builds so that `pgopt --version` always
// returns something meaningful.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

type flags struct {
	dbURL       string
	rulesSpec   string
	format      string
	minSeverity string
	failOn      string
	color       string
	quiet       bool
	verbose     bool
	useExplain  bool
	listRules   bool
	jsRules      bool
	jsRulesDir   string
	ignoreFile   string
	noIgnoreFile bool
	showVersion  bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var f flags
	fs := flag.NewFlagSet("pgopt", flag.ContinueOnError)
	fs.SetOutput(stderr)

	fs.StringVar(&f.dbURL, "db", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL (postgres://...) — enables schema/explain rules")
	fs.StringVar(&f.rulesSpec, "rules", "all", "rule selection: 'all', 'r1,r2', or 'all,-r3' to exclude")
	fs.StringVar(&f.format, "format", "text", "output format: text | json")
	fs.StringVar(&f.minSeverity, "min-severity", "info", "omit findings below this severity: info | warn | error")
	fs.StringVar(&f.failOn, "fail-on", "warn", "exit non-zero if any finding >= this severity: info | warn | error")
	fs.StringVar(&f.color, "color", "auto", "color output: auto | always | never")
	fs.BoolVar(&f.quiet, "quiet", false, "suppress all stdout; use exit code only")
	fs.BoolVar(&f.verbose, "verbose", false, "include explanations and source evidence for each finding")
	fs.BoolVar(&f.useExplain, "explain", false, "run EXPLAIN (ANALYZE, BUFFERS) against --db and include findings (runs inside a READ ONLY tx)")
	fs.BoolVar(&f.listRules, "list-rules", false, "print all registered rules with descriptions and exit")
	fs.BoolVar(&f.jsRules, "js-rules", false, "enable user-defined JavaScript rules loaded from --js-rules-dir")
	fs.StringVar(&f.jsRulesDir, "js-rules-dir", "rules-js", "directory scanned for JS rules when --js-rules is set")
	fs.StringVar(&f.ignoreFile, "ignore-file", ".pgoptignore", "file listing rule IDs to skip (one per line, '#' for comments). Loaded from the working directory by default.")
	fs.BoolVar(&f.noIgnoreFile, "no-ignore-file", false, "do not read an ignore file even if one exists")
	fs.BoolVar(&f.showVersion, "version", false, "print pgopt version and exit")

	fs.Usage = func() {
		fmt.Fprintf(stderr, `pgopt — PostgreSQL query optimizer / advisor

USAGE
  pgopt [flags] <file.sql | - >
  cat query.sql | pgopt [flags] -
  pgopt --list-rules

FLAGS
`)
		fs.PrintDefaults()
		fmt.Fprintf(stderr, `
EXAMPLES
  pgopt query.sql
  cat q.sql | pgopt -
  pgopt --db "$DATABASE_URL" --explain --verbose query.sql
  pgopt --format json --rules "all,-select_star" query.sql
  pgopt --list-rules

EXIT CODES
  0  no findings >= --fail-on
  1  findings >= --fail-on detected
  2  SQL parse error
  3  config / I/O error
`)
	}

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitConfig
		}
		return exitConfig
	}

	if f.showVersion {
		printVersion(stdout)
		return exitOK
	}

	// Optional JS rules are loaded up front so that --list-rules sees them
	// and so --rules selection spans the combined set.
	var jsEngine *jsrules.Engine
	if f.jsRules {
		eng, err := jsrules.LoadDir(f.jsRulesDir)
		if err != nil {
			fmt.Fprintln(stderr, "pgopt: --js-rules:", err)
			return exitConfig
		}
		jsEngine = eng
	}

	if f.listRules {
		listRules(stdout, jsEngine)
		return exitOK
	}

	minSev, err := analyzer.ParseSeverity(f.minSeverity)
	if err != nil {
		fmt.Fprintln(stderr, "pgopt: --min-severity:", err)
		return exitConfig
	}
	failSev, err := analyzer.ParseSeverity(f.failOn)
	if err != nil {
		fmt.Fprintln(stderr, "pgopt: --fail-on:", err)
		return exitConfig
	}
	selected, err := selectRules(f.rulesSpec, jsEngine)
	if err != nil {
		fmt.Fprintln(stderr, "pgopt: --rules:", err)
		return exitConfig
	}
	if !f.noIgnoreFile {
		ignored, err := loadIgnoreFile(f.ignoreFile)
		if err != nil {
			fmt.Fprintln(stderr, "pgopt: --ignore-file:", err)
			return exitConfig
		}
		if len(ignored) > 0 {
			selected = applyIgnore(selected, ignored)
		}
	}

	// Read input
	var sql string
	switch fs.NArg() {
	case 0:
		// expect stdin
		b, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(stderr, "pgopt: read stdin:", err)
			return exitConfig
		}
		sql = string(b)
	case 1:
		arg := fs.Arg(0)
		if arg == "-" {
			b, err := io.ReadAll(stdin)
			if err != nil {
				fmt.Fprintln(stderr, "pgopt: read stdin:", err)
				return exitConfig
			}
			sql = string(b)
		} else {
			b, err := os.ReadFile(arg)
			if err != nil {
				fmt.Fprintln(stderr, "pgopt: read", arg, ":", err)
				return exitConfig
			}
			sql = string(b)
		}
	default:
		fs.Usage()
		return exitConfig
	}

	sql = strings.TrimSpace(sql)
	if sql == "" {
		fmt.Fprintln(stderr, "pgopt: empty input")
		return exitConfig
	}

	// Parse
	ast, err := analyzer.ParseJSON(sql)
	if err != nil {
		fmt.Fprintln(stderr, "pgopt: parse error:", err)
		return exitParse
	}

	ctx := &analyzer.Context{
		Source: sql,
		AST:    ast,
	}

	// Optionally load schema and/or run EXPLAIN. Both respect SIGINT so
	// the user can bail out of a long EXPLAIN without leaving the
	// backend running — the pgx driver cancels the in-flight query when
	// the context is cancelled, and we surface a clean message instead
	// of a traceback.
	dbCtx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if f.dbURL != "" {
		sch, err := schema.Load(dbCtx, f.dbURL)
		if err != nil {
			if dbCtx.Err() != nil {
				fmt.Fprintln(stderr, "pgopt: cancelled")
				return exitConfig
			}
			fmt.Fprintln(stderr, "pgopt: schema load failed:", redact(err.Error()))
			if !f.useExplain {
				// keep going, just without schema
			}
		} else {
			ctx.Schema = sch
		}
	}
	if f.useExplain {
		if f.dbURL == "" {
			fmt.Fprintln(stderr, "pgopt: --explain requires --db")
			return exitConfig
		}
		plan, err := explain.Run(dbCtx, f.dbURL, sql)
		if err != nil {
			if dbCtx.Err() != nil {
				fmt.Fprintln(stderr, "pgopt: EXPLAIN cancelled")
				return exitConfig
			}
			fmt.Fprintln(stderr, "pgopt: EXPLAIN failed:", redact(err.Error()))
		} else {
			ctx.Explain = plan
		}
	}

	// Run rules, then apply inline pragma filtering. Pragmas take effect
	// after rules fire, so a rule can still observe the pattern and we
	// simply hide the finding the user has marked ignored.
	findings := analyzer.Run(ctx, selected)
	pragmas := analyzer.ParsePragmas(sql)
	findings = analyzer.FilterFindings(findings, pragmas)

	// Filter by min severity.
	filtered := make(analyzer.FindingList, 0, len(findings))
	for _, x := range findings {
		if x.Severity >= minSev {
			filtered = append(filtered, x)
		}
	}

	// Render.
	if !f.quiet {
		switch f.format {
		case "json":
			if err := report.JSON(stdout, filtered); err != nil {
				fmt.Fprintln(stderr, "pgopt: JSON render:", err)
				return exitConfig
			}
		case "text", "":
			report.Text(stdout, filtered, report.TextOptions{
				Color:   resolveColor(f.color, stdout),
				Verbose: f.verbose,
				Source:  sql,
			})
		default:
			fmt.Fprintln(stderr, "pgopt: unknown format:", f.format)
			return exitConfig
		}
	}

	if filtered.MaxSeverity() >= failSev && len(filtered) > 0 {
		return exitFinds
	}
	return exitOK
}

// ruleEntry is the minimal shape needed to print a rule listing. It covers
// both Go rules (from the internal/rules registry) and JS rules (loaded
// from disk) without leaking either type across the CLI layer.
type ruleEntry struct {
	id, description string
	schema, explain bool
	js              bool
}

func listRules(w io.Writer, jsEngine *jsrules.Engine) {
	entries := collectEntries(jsEngine)
	fmt.Fprintf(w, "Registered rules (%d):\n\n", len(entries))
	for _, r := range entries {
		tag := ""
		if r.schema {
			tag += " [schema]"
		}
		if r.explain {
			tag += " [explain]"
		}
		if r.js {
			tag += " [js]"
		}
		fmt.Fprintf(w, "  %-32s %s%s\n", r.id, r.description, tag)
	}
}

func collectEntries(jsEngine *jsrules.Engine) []ruleEntry {
	var out []ruleEntry
	for _, r := range rules.All() {
		out = append(out, ruleEntry{
			id: r.ID(), description: r.Description(),
			schema: r.RequiresSchema(), explain: r.RequiresExplain(),
		})
	}
	if jsEngine != nil {
		for _, r := range jsEngine.Rules() {
			out = append(out, ruleEntry{
				id: r.ID(), description: r.Description(),
				schema: r.RequiresSchema(), explain: r.RequiresExplain(),
				js: true,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// selectRules parses the --rules spec ("all", "a,b", "all,-c") and applies
// it across the union of Go rules and JS rules (if an engine is provided).
// JS rules appear in the selection alongside Go rules, so a single spec
// controls both.
func selectRules(spec string, jsEngine *jsrules.Engine) ([]analyzer.Runnable, error) {
	combined := map[string]analyzer.Runnable{}
	order := []string{}
	for _, r := range rules.All() {
		combined[r.ID()] = r
		order = append(order, r.ID())
	}
	if jsEngine != nil {
		for _, r := range jsEngine.Rules() {
			if _, dup := combined[r.ID()]; dup {
				return nil, fmt.Errorf("js rule %q collides with built-in rule", r.ID())
			}
			combined[r.ID()] = r
			order = append(order, r.ID())
		}
	}
	sort.Strings(order)

	enabled := map[string]bool{}
	tokens := splitTrim(spec, ',')
	if len(tokens) == 0 || tokens[0] == "" || tokens[0] == "all" {
		for id := range combined {
			enabled[id] = true
		}
		if len(tokens) > 0 && (tokens[0] == "" || tokens[0] == "all") {
			tokens = tokens[1:]
		}
	}
	for _, t := range tokens {
		if t == "" {
			continue
		}
		if t[0] == '-' {
			delete(enabled, t[1:])
			continue
		}
		if _, ok := combined[t]; !ok {
			return nil, fmt.Errorf("unknown rule: %s", t)
		}
		enabled[t] = true
	}

	out := make([]analyzer.Runnable, 0, len(enabled))
	for _, id := range order {
		if enabled[id] {
			out = append(out, combined[id])
		}
	}
	return out, nil
}

func splitTrim(s string, sep rune) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == sep {
			out = append(out, strings.TrimSpace(cur))
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, strings.TrimSpace(cur))
	return out
}

// loadIgnoreFile reads a newline-delimited list of rule IDs to skip.
// Lines starting with '#' and blank lines are ignored. A missing file
// at the default path is not an error — the whole feature is opt-in by
// the file's presence. A missing file at an explicitly provided path
// is an error so typos surface early.
func loadIgnoreFile(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && path == ".pgoptignore" {
			return nil, nil
		}
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out, nil
}

// applyIgnore drops any rule whose ID is listed in ignored.
func applyIgnore(rules []analyzer.Runnable, ignored map[string]bool) []analyzer.Runnable {
	out := rules[:0]
	for _, r := range rules {
		if ignored[r.ID()] {
			continue
		}
		out = append(out, r)
	}
	return out
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "pgopt %s", Version)
	if Commit != "" {
		fmt.Fprintf(w, " (%s)", Commit)
	}
	if Date != "" {
		fmt.Fprintf(w, " built %s", Date)
	}
	fmt.Fprintln(w)
}

func resolveColor(mode string, w io.Writer) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}
	// auto
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
