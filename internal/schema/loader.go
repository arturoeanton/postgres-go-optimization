package schema

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Load connects to the database and returns a snapshot of the schema.
// Only user schemas (excluding pg_catalog and information_schema) are loaded.
//
// The load is best-effort: partial data is returned even if some queries
// fail (e.g. restricted access to pg_statistic). The returned error is
// non-nil only if the initial connection fails or the first query fails.
func Load(ctx context.Context, url string) (*Schema, error) {
	conn, err := open(ctx, url)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	s := &Schema{Relations: map[string]*Relation{}}

	if err := loadRelations(ctx, conn, s); err != nil {
		return nil, fmt.Errorf("load relations: %w", err)
	}
	// These are best-effort; log but do not fail.
	_ = loadColumns(ctx, conn, s)
	_ = loadIndexes(ctx, conn, s)
	_ = loadStats(ctx, conn, s)
	_ = loadPartitioning(ctx, conn, s)
	_ = loadForeignKeys(ctx, conn, s)
	_ = loadIndexUsage(ctx, conn, s)
	_ = loadPrimaryKeyFlag(ctx, conn, s)

	return s, nil
}

const fkSQL = `
SELECT n.nspname, c.relname, con.conname,
       ARRAY(SELECT a.attname FROM unnest(con.conkey) AS k
              JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k) AS cols,
       rn.nspname AS refschema,
       rc.relname AS reftable,
       ARRAY(SELECT a.attname FROM unnest(con.confkey) AS k
              JOIN pg_attribute a ON a.attrelid = rc.oid AND a.attnum = k) AS refcols
  FROM pg_constraint con
  JOIN pg_class c  ON c.oid  = con.conrelid
  JOIN pg_namespace n  ON n.oid  = c.relnamespace
  JOIN pg_class rc ON rc.oid = con.confrelid
  JOIN pg_namespace rn ON rn.oid = rc.relnamespace
 WHERE con.contype = 'f'
   AND n.nspname NOT IN ('pg_catalog','information_schema')
`

func loadForeignKeys(ctx context.Context, conn *pgx.Conn, s *Schema) error {
	rows, err := conn.Query(ctx, fkSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sch, rel, name, rsch, rtbl string
		var cols, refcols []string
		if err := rows.Scan(&sch, &rel, &name, &cols, &rsch, &rtbl, &refcols); err != nil {
			continue
		}
		r, ok := s.Relations[qual(sch, rel)]
		if !ok {
			continue
		}
		r.ForeignKeys = append(r.ForeignKeys, ForeignKey{
			Name:       name,
			Columns:    cols,
			RefSchema:  rsch,
			RefTable:   rtbl,
			RefColumns: refcols,
		})
	}
	return rows.Err()
}

const indexUsageSQL = `
SELECT schemaname, relname, indexrelname, idx_scan
  FROM pg_stat_user_indexes
`

func loadIndexUsage(ctx context.Context, conn *pgx.Conn, s *Schema) error {
	rows, err := conn.Query(ctx, indexUsageSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sch, rel, idx string
		var scans int64
		if err := rows.Scan(&sch, &rel, &idx, &scans); err != nil {
			continue
		}
		r, ok := s.Relations[qual(sch, rel)]
		if !ok {
			continue
		}
		if r.IndexScans == nil {
			r.IndexScans = map[string]int64{}
		}
		r.IndexScans[idx] = scans
	}
	return rows.Err()
}

// loadPrimaryKeyFlag just sets Relation.HasPrimary based on pg_index.indisprimary.
func loadPrimaryKeyFlag(ctx context.Context, conn *pgx.Conn, s *Schema) error {
	for _, rel := range s.Relations {
		for _, idx := range rel.Indexes {
			if idx.Primary {
				rel.HasPrimary = true
				break
			}
		}
	}
	return nil
}

// We cast relkind (pg_class."char") to text so pgx can Scan into *string;
// the native "char" OID doesn't decode to Go string by default.
const relationsSQL = `
SELECT n.nspname, c.relname, c.relkind::text,
       COALESCE(c.relpages, 0) AS relpages,
       COALESCE(c.reltuples, 0) AS reltuples
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE c.relkind IN ('r','v','m','p','f')
   AND n.nspname NOT IN ('pg_catalog','information_schema')
   AND n.nspname !~ '^pg_toast'
`

func loadRelations(ctx context.Context, conn *pgx.Conn, s *Schema) error {
	rows, err := conn.Query(ctx, relationsSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r Relation
		if err := rows.Scan(&r.Schema, &r.Name, &r.Kind, &r.RelPages, &r.RelTuples); err != nil {
			return err
		}
		s.Relations[qual(r.Schema, r.Name)] = &r
	}
	return rows.Err()
}

const columnsSQL = `
SELECT n.nspname, c.relname, a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod),
       a.attnotnull, a.atthasdef
  FROM pg_attribute a
  JOIN pg_class c ON c.oid = a.attrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE a.attnum > 0 AND NOT a.attisdropped
   AND c.relkind IN ('r','v','m','p','f')
   AND n.nspname NOT IN ('pg_catalog','information_schema')
   AND n.nspname !~ '^pg_toast'
 ORDER BY n.nspname, c.relname, a.attnum
`

func loadColumns(ctx context.Context, conn *pgx.Conn, s *Schema) error {
	rows, err := conn.Query(ctx, columnsSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sch, rel string
		var c Column
		if err := rows.Scan(&sch, &rel, &c.Name, &c.TypeName, &c.NotNull, &c.HasDef); err != nil {
			continue
		}
		if r, ok := s.Relations[qual(sch, rel)]; ok {
			r.Columns = append(r.Columns, c)
		}
	}
	return rows.Err()
}

const indexesSQL = `
SELECT n.nspname, t.relname, i.relname,
       ix.indisunique, ix.indisprimary,
       am.amname,
       pg_get_indexdef(ix.indexrelid) AS def,
       COALESCE(pg_get_expr(ix.indpred, ix.indrelid), '') AS pred
  FROM pg_index ix
  JOIN pg_class i ON i.oid = ix.indexrelid
  JOIN pg_class t ON t.oid = ix.indrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
  JOIN pg_am am ON am.oid = i.relam
 WHERE n.nspname NOT IN ('pg_catalog','information_schema')
`

func loadIndexes(ctx context.Context, conn *pgx.Conn, s *Schema) error {
	rows, err := conn.Query(ctx, indexesSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sch, rel, idxName, method, def, pred string
		var uniq, prim bool
		if err := rows.Scan(&sch, &rel, &idxName, &uniq, &prim, &method, &def, &pred); err != nil {
			continue
		}
		r, ok := s.Relations[qual(sch, rel)]
		if !ok {
			continue
		}
		idx := Index{
			Name:      idxName,
			Unique:    uniq,
			Primary:   prim,
			Method:    method,
			Predicate: pred,
		}
		// Parse columns and INCLUDE from the CREATE INDEX text. The definition
		// has the form "CREATE [UNIQUE] INDEX ... USING method (cols) [INCLUDE (cols)] [WHERE ...]"
		idx.Columns, idx.Include = parseIndexDef(def)
		r.Indexes = append(r.Indexes, idx)
	}
	return rows.Err()
}

const statsSQL = `
SELECT schemaname, relname, n_live_tup, n_dead_tup, last_autoanalyze, last_autovacuum
  FROM pg_stat_user_tables
`

func loadStats(ctx context.Context, conn *pgx.Conn, s *Schema) error {
	rows, err := conn.Query(ctx, statsSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sch, rel string
		var live, dead int64
		var la, lv *string
		if err := rows.Scan(&sch, &rel, &live, &dead, &la, &lv); err != nil {
			continue
		}
		r, ok := s.Relations[qual(sch, rel)]
		if !ok {
			continue
		}
		r.LiveTuples = live
		r.DeadTuples = dead
		if live > 0 {
			r.NDeadRatio = float64(dead) / float64(live)
		}
		r.LastAnalyze = la
		r.LastVacuum = lv
	}
	return rows.Err()
}

const partSQL = `
SELECT n.nspname, c.relname, pn.nspname, pc.relname
  FROM pg_inherits i
  JOIN pg_class c ON c.oid = i.inhrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_class pc ON pc.oid = i.inhparent
  JOIN pg_namespace pn ON pn.oid = pc.relnamespace
 WHERE pc.relkind = 'p'
`

func loadPartitioning(ctx context.Context, conn *pgx.Conn, s *Schema) error {
	rows, err := conn.Query(ctx, partSQL)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ns, nm, pns, pnm string
		if err := rows.Scan(&ns, &nm, &pns, &pnm); err != nil {
			continue
		}
		if r, ok := s.Relations[qual(ns, nm)]; ok {
			r.PartitionOf = qual(pns, pnm)
		}
	}
	return rows.Err()
}

// parseIndexDef extracts (keys, include) from a CREATE INDEX statement.
// This is a pragmatic parse: it finds the first "(...)" group (keys) and
// an optional following "INCLUDE (...)" group.
func parseIndexDef(def string) (keys, include []string) {
	// find first '(' then matching ')'
	start := indexOf(def, '(')
	if start < 0 {
		return
	}
	depth := 0
	end := -1
	for i := start; i < len(def); i++ {
		switch def[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return
	}
	keys = splitTopColumns(def[start+1 : end])

	// INCLUDE (...) ?
	tail := def[end+1:]
	if off := indexOfLower(tail, "include"); off >= 0 {
		ps := indexFrom(tail, off, '(')
		if ps < 0 {
			return
		}
		depth = 0
		pe := -1
		for i := ps; i < len(tail); i++ {
			switch tail[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					pe = i
				}
			}
			if pe >= 0 {
				break
			}
		}
		if pe > ps {
			include = splitTopColumns(tail[ps+1 : pe])
		}
	}
	return
}

// helpers — hand-rolled to avoid pulling in strings.* here (keeps the
// file self-contained and testable).

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func indexFrom(s string, from int, c byte) int {
	for i := from; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func indexOfLower(s, sub string) int {
	n, m := len(s), len(sub)
	for i := 0; i+m <= n; i++ {
		match := true
		for j := 0; j < m; j++ {
			a := s[i+j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if a != sub[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func splitTopColumns(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, stripCol(s[start:i]))
				start = i + 1
			}
		}
	}
	out = append(out, stripCol(s[start:]))
	return out
}

// stripCol removes whitespace and quotes, and drops trailing ordering/opclass
// decorations like "col ASC NULLS LAST" -> "col" or "\"Mixed Case\" DESC" -> "Mixed Case".
func stripCol(s string) string {
	// trim outer whitespace
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	j := len(s)
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	s = s[i:j]
	if s == "" {
		return s
	}
	// If the identifier is quoted, take everything up to the matching quote.
	if s[0] == '"' {
		end := 1
		for end < len(s) && s[end] != '"' {
			end++
		}
		if end < len(s) {
			return s[1:end]
		}
		return s[1:]
	}
	// Otherwise, cut at the first whitespace (strips ASC/DESC/NULLS decorators).
	for k := 0; k < len(s); k++ {
		if s[k] == ' ' || s[k] == '\t' {
			return s[:k]
		}
	}
	return s
}
