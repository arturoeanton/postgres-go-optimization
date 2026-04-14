-- expect: distinct_on_joins
SELECT DISTINCT u.id, u.email
  FROM users u
  JOIN orders o ON o.user_id = u.id
 WHERE o.status = 'paid';
