// Package jsrules loads user-defined rules written in JavaScript and adapts
// them to the rules.Rule interface. Each rule lives in its own directory
// under a root (default rules-js/) with the shape:
//
//	rules-js/
//	    <rule_id>/
//	        manifest.json   // id, description, severity, requiresSchema, requiresExplain, evidence
//	        main.js         // exports a check(ctx) function
//
// Rules are opt-in: they are only discovered and executed when the caller
// explicitly enables them (see cmd/pgopt --js-rules). The JavaScript engine
// is goja (pure Go), and heavy AST work is delegated to Go-side helpers
// bound as the global `pg` object. See docs/js-rules.md for the full API.
package jsrules
