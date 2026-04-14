-- expect: js_double_negation
SELECT id FROM users WHERE NOT (NOT active);
