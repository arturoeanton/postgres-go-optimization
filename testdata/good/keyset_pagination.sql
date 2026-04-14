-- expect: (none)
SELECT id, email FROM users
 WHERE id > 1000
 ORDER BY id
 LIMIT 50;
