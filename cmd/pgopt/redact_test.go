package main

import (
	"strings"
	"testing"
)

func TestRedact_URLCredentials(t *testing.T) {
	in := "failed to dial postgres://alice:s3cret@db.example.com:5432/app?sslmode=disable: timeout"
	out := redact(in)
	if strings.Contains(out, "s3cret") {
		t.Errorf("password leaked: %s", out)
	}
	if !strings.Contains(out, "alice:***@") {
		t.Errorf("expected redacted userinfo alice:***@, got: %s", out)
	}
}

func TestRedact_KVPassword(t *testing.T) {
	in := "connection refused: host=db user=alice password=topsecret dbname=app"
	out := redact(in)
	if strings.Contains(out, "topsecret") {
		t.Errorf("password leaked: %s", out)
	}
	if !strings.Contains(out, "password=***") {
		t.Errorf("expected password=***: %s", out)
	}
}

func TestRedact_LeavesSafeStrings(t *testing.T) {
	in := "EXPLAIN failed: relation 'users' does not exist"
	if got := redact(in); got != in {
		t.Errorf("safe string mutated: %q", got)
	}
}

func TestRedactDSN_Valid(t *testing.T) {
	in := "postgres://alice:s3cret@db.example.com:5432/app?sslmode=disable"
	out := redactDSN(in)
	if strings.Contains(out, "s3cret") {
		t.Errorf("redactDSN leaked password: %s", out)
	}
	if !strings.Contains(out, "alice") || !strings.Contains(out, "@db.example.com") {
		t.Errorf("redactDSN lost unrelated URL parts: %s", out)
	}
}

func TestRedactDSN_NonURL(t *testing.T) {
	in := "host=db user=alice password=top dbname=app"
	out := redactDSN(in)
	if strings.Contains(out, "top dbname") && !strings.Contains(out, "password=***") {
		t.Errorf("kv DSN not redacted: %s", out)
	}
}

func TestRedact_URLWithoutPassword(t *testing.T) {
	in := "dialing postgres://alice@db.example.com:5432/app"
	out := redact(in)
	if !strings.Contains(out, "alice:***@") {
		t.Errorf("user-only URL should still get masked: %s", out)
	}
}
