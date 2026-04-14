-- expect: js_redundant_coalesce
SELECT COALESCE(email, email, 'n/a') FROM users;
