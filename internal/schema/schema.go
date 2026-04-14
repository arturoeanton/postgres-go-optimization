// Package schema loads a minimal, read-only view of the PostgreSQL catalog.
//
// We only read what the rules need: relations (tables + columns + types),
// indexes (with their columns and INCLUDE columns), partitioning info, and
// pg_stat_user_tables / pg_stats for maintenance and selectivity hints.
//
// All queries run with the user's effective search_path and respect any
// role-level visibility. If you need to analyze schema-protected relations,
// connect with the appropriate role.
package schema

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Schema is the in-memory projection of what we read from the catalog.
type Schema struct {
	Relations map[string]*Relation // key: "schema.name" (lowercased)
}

// Relation is a table, view, matview, partitioned table, or foreign table.
type Relation struct {
	Schema      string
	Name        string
	Kind        string // r, v, m, p, f (pg_class.relkind)
	Columns     []Column
	Indexes     []Index
	PartitionOf string   // parent, if this is a partition
	PartKeys    []string // partition keys if RelKind == 'p'

	// maintenance stats
	LiveTuples   int64
	DeadTuples   int64
	NDeadRatio   float64
	LastAnalyze  *string
	LastVacuum   *string
	RelPages     int64
	RelTuples    float64
	HasPrimary   bool

	// foreign keys declared on this table
	ForeignKeys []ForeignKey

	// per-index usage stats (pg_stat_user_indexes.idx_scan)
	IndexScans map[string]int64
}

// Column is one attribute of a Relation.
type Column struct {
	Name     string
	TypeName string
	NotNull  bool
	HasDef   bool
}

// ForeignKey describes a single FK constraint on this relation.
type ForeignKey struct {
	Name        string
	Columns     []string // columns on THIS table
	RefSchema   string
	RefTable    string
	RefColumns  []string
}

// AllForeignKeys returns the FKs attached to rel. Stored on Relation so
// rules can inspect them without another catalog query.
var _ = struct{}{} // anchor for future additions

// Index is a registered index on a Relation.
type Index struct {
	Name      string
	Unique    bool
	Primary   bool
	Method    string   // btree, gin, gist, brin, hash, spgist
	Columns   []string // key columns (ordered)
	Include   []string // INCLUDE columns (PG11+)
	Predicate string   // partial index WHERE clause, if any
}

// Qualified key helper.
func qual(schema, name string) string {
	return fmt.Sprintf("%s.%s", schema, name)
}

// Lookup finds a relation by unqualified or qualified name.
// Matches public schema by default when unqualified.
func (s *Schema) Lookup(name string) *Relation {
	if r, ok := s.Relations[name]; ok {
		return r
	}
	return s.Relations["public."+name]
}

// Must connect with READ-ONLY semantics: we never issue anything but SELECT.
// Caller closes conn.
func open(ctx context.Context, url string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	// Fail-safe: set this session read-only.
	if _, err := conn.Exec(ctx, "SET default_transaction_read_only = on"); err != nil {
		conn.Close(ctx)
		return nil, fmt.Errorf("enforce read-only: %w", err)
	}
	return conn, nil
}
