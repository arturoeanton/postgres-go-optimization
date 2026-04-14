package main

import (
	"net/url"
	"regexp"
	"strings"
)

// redact strips secrets out of a string before we print it to stderr.
// We target two shapes:
//
//   - DSN URLs with embedded credentials:
//     postgres://user:pass@host:port/db?sslmode=disable
//   - key-value connection strings in "postgres=..." form that pgx
//     echoes back in error messages, e.g. password=secret.
//
// The goal is to make it safe to run pgopt in CI logs and shared
// terminals without leaking the password set on --db.
func redact(msg string) string {
	if msg == "" {
		return msg
	}
	msg = redactURLCredentials(msg)
	msg = redactKVPassword(msg)
	return msg
}

// urlRe matches a postgres-ish URL with userinfo. The non-greedy user and
// password captures keep us from swallowing unrelated text.
var urlRe = regexp.MustCompile(`(?i)\b(postgres(?:ql)?://)([^\s:@/]+)(?::[^\s@/]*)?@`)

func redactURLCredentials(s string) string {
	return urlRe.ReplaceAllStringFunc(s, func(m string) string {
		idx := strings.Index(m, "://")
		if idx < 0 {
			return m
		}
		scheme := m[:idx+3]
		rest := m[idx+3:]
		// rest ends with '@'
		rest = strings.TrimSuffix(rest, "@")
		u := rest
		if at := strings.LastIndex(rest, ":"); at >= 0 {
			u = rest[:at]
		}
		return scheme + u + ":***@"
	})
}

// kvPasswordRe matches pgx-style key/value fragments.
var kvPasswordRe = regexp.MustCompile(`(?i)\bpassword\s*=\s*([^\s]+)`)

func redactKVPassword(s string) string {
	return kvPasswordRe.ReplaceAllStringFunc(s, func(m string) string {
		eq := strings.Index(m, "=")
		if eq < 0 {
			return m
		}
		return m[:eq+1] + "***"
	})
}

// redactDSN takes a full DSN and returns a version safe to log. It is
// used when pgopt echoes the user's --db back (e.g. in "schema load
// failed" lines).
func redactDSN(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil {
			if u.User != nil {
				u.User = url.UserPassword(u.User.Username(), "***")
			}
			return u.String()
		}
	}
	return redact(dsn)
}
