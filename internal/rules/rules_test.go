package rules

import (
	"testing"

	"github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

// runOne parses src, runs one rule, and returns its findings.
func runOne(t *testing.T, rule Rule, src string) []analyzer.Finding {
	t.Helper()
	ast, err := analyzer.ParseJSON(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return rule.Check(&analyzer.Context{Source: src, AST: ast})
}

type ruleCase struct {
	name    string
	sql     string
	fires   bool
}

// TestRules runs every rule against a matrix of positive (should fire) and
// negative (should not fire) cases. Schema/explain-dependent rules are
// tested separately in dedicated test files or integration tests.
func TestRules(t *testing.T) {
	cases := map[string][]ruleCase{
		"select_star": {
			{"star_top", "SELECT * FROM t", true},
			{"star_qualified", "SELECT t.* FROM t", true},
			{"exists_star_ok", "SELECT 1 FROM t WHERE EXISTS(SELECT * FROM s)", false},
			{"explicit_cols", "SELECT a, b FROM t", false},
		},
		"offset_pagination": {
			{"big_offset", "SELECT 1 FROM t LIMIT 10 OFFSET 100000", true},
			{"small_offset", "SELECT 1 FROM t LIMIT 10 OFFSET 10", false},
			{"no_offset", "SELECT 1 FROM t LIMIT 10", false},
		},
		"not_in_null": {
			{"not_in_subq", "SELECT a FROM t WHERE a NOT IN (SELECT b FROM s)", true},
			{"not_in_literal", "SELECT a FROM t WHERE a NOT IN (1,2,3)", false},
			{"not_exists", "SELECT a FROM t WHERE NOT EXISTS(SELECT 1 FROM s WHERE s.b=t.a)", false},
		},
		"cast_in_where": {
			{"cast_date", "SELECT 1 FROM t WHERE ts::date = '2026-01-01'", true},
			{"no_cast", "SELECT 1 FROM t WHERE ts >= '2026-01-01'", false},
		},
		"function_on_column": {
			{"lower_col", "SELECT 1 FROM t WHERE lower(email) = 'a@b.com'", true},
			{"abs_col", "SELECT 1 FROM t WHERE abs(balance) > 100", true},
			{"date_trunc", "SELECT 1 FROM t WHERE date_trunc('day', ts) = '2026-01-01'", true},
			{"no_wrapper", "SELECT 1 FROM t WHERE email = 'a@b.com'", false},
			{"function_of_const", "SELECT 1 FROM t WHERE email = lower('A@B.COM')", false},
		},
		"like_leading_wildcard": {
			{"leading_wildcard", "SELECT 1 FROM t WHERE name LIKE '%foo%'", true},
			{"leading_only", "SELECT 1 FROM t WHERE name LIKE '%foo'", true},
			{"trailing_only", "SELECT 1 FROM t WHERE name LIKE 'foo%'", false},
			{"ilike_leading", "SELECT 1 FROM t WHERE name ILIKE '%foo'", true},
		},
		"large_in_list": {
			{"big_list", "SELECT 1 FROM t WHERE id IN (" + repeatInt(150) + ")", true},
			{"small_list", "SELECT 1 FROM t WHERE id IN (1,2,3,4,5)", false},
		},
		"missing_where": {
			{"update_no_where", "UPDATE t SET a = 1", true},
			{"delete_no_where", "DELETE FROM t", true},
			{"update_with_where", "UPDATE t SET a=1 WHERE id=5", false},
			{"delete_with_where", "DELETE FROM t WHERE id=5", false},
		},
		"not_sargable": {
			{"plus_const", "SELECT 1 FROM t WHERE col + 1 > 100", true},
			{"times_const", "SELECT 1 FROM t WHERE col * 2 > 100", true},
			{"concat", "SELECT 1 FROM t WHERE col || 'x' = 'yx'", true},
			{"clean", "SELECT 1 FROM t WHERE col > 99", false},
		},
		"select_for_update_no_limit": {
			{"no_limit", "SELECT 1 FROM t WHERE a=1 FOR UPDATE", true},
			{"with_limit", "SELECT 1 FROM t WHERE a=1 FOR UPDATE LIMIT 10", false},
			{"no_lock", "SELECT 1 FROM t WHERE a=1", false},
		},
		"distinct_on_joins": {
			{"distinct_join", "SELECT DISTINCT u.id FROM u JOIN o ON o.uid=u.id", true},
			{"distinct_no_join", "SELECT DISTINCT id FROM t", false},
			{"join_no_distinct", "SELECT u.id FROM u JOIN o ON o.uid=u.id", false},
		},
	}

	for ruleID, ccs := range cases {
		r, ok := Get(ruleID)
		if !ok {
			t.Fatalf("rule %q not registered", ruleID)
		}
		for _, c := range ccs {
			c := c
			t.Run(ruleID+"/"+c.name, func(t *testing.T) {
				got := runOne(t, r, c.sql)
				fired := len(got) > 0
				if fired != c.fires {
					t.Errorf("rule=%s case=%s fired=%v want=%v\n  SQL: %s",
						ruleID, c.name, fired, c.fires, c.sql)
					for _, f := range got {
						t.Logf("  finding: %s", f.Message)
					}
				}
			})
		}
	}
}

// Build a comma-separated literal list of integers "1,2,3,...,n".
func repeatInt(n int) string {
	out := ""
	for i := 1; i <= n; i++ {
		if i > 1 {
			out += ","
		}
		out += itoa(i)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestSelect_RulesSpec(t *testing.T) {
	// "all" => every rule
	r, err := Select("all")
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != len(All()) {
		t.Errorf("expected all rules, got %d", len(r))
	}

	// include list
	r, err = Select("select_star,offset_pagination")
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 2 {
		t.Errorf("expected 2, got %d", len(r))
	}

	// "all,-x" removes one
	r, err = Select("all,-select_star")
	if err != nil {
		t.Fatal(err)
	}
	all := len(All())
	if len(r) != all-1 {
		t.Errorf("expected %d, got %d", all-1, len(r))
	}

	// unknown rule errors
	if _, err := Select("does_not_exist"); err == nil {
		t.Error("expected error for unknown rule")
	}
}
