-- Demo: multi-anti-patrón contra el schema del docker.
-- Correr:
--   ./pgopt --db "$PGOPT_DB" --explain --verbose testdata/demo/bad_query_1.sql
SELECT *
  FROM users u
  JOIN orders o ON o.user_id = u.id
 WHERE lower(u.email) LIKE '%@example.com%'
   AND o.created_at::date = CURRENT_DATE
 ORDER BY u.id
 LIMIT 50
 OFFSET 100000;
