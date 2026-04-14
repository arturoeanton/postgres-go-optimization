package schema

import "testing"

func TestParseIndexDef_Simple(t *testing.T) {
	keys, inc := parseIndexDef("CREATE INDEX t_a_idx ON public.t USING btree (a)")
	if len(keys) != 1 || keys[0] != "a" {
		t.Errorf("keys=%v", keys)
	}
	if len(inc) != 0 {
		t.Errorf("include=%v", inc)
	}
}

func TestParseIndexDef_MultiColumn(t *testing.T) {
	keys, _ := parseIndexDef("CREATE INDEX t_a_b_idx ON t USING btree (a, b DESC)")
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Errorf("keys=%v", keys)
	}
}

func TestParseIndexDef_Include(t *testing.T) {
	keys, inc := parseIndexDef("CREATE INDEX x ON t USING btree (a) INCLUDE (b, c)")
	if len(keys) != 1 || keys[0] != "a" {
		t.Errorf("keys=%v", keys)
	}
	if len(inc) != 2 || inc[0] != "b" || inc[1] != "c" {
		t.Errorf("include=%v", inc)
	}
}

func TestParseIndexDef_QuotedIdent(t *testing.T) {
	keys, _ := parseIndexDef(`CREATE INDEX x ON t USING btree ("Mixed Case")`)
	if len(keys) != 1 || keys[0] != "Mixed Case" {
		t.Errorf("keys=%v", keys)
	}
}

func TestSchemaLookup(t *testing.T) {
	s := &Schema{Relations: map[string]*Relation{
		"public.users": {Schema: "public", Name: "users"},
	}}
	if s.Lookup("users") == nil {
		t.Error("unqualified lookup failed")
	}
	if s.Lookup("public.users") == nil {
		t.Error("qualified lookup failed")
	}
	if s.Lookup("missing") != nil {
		t.Error("missing should be nil")
	}
}
