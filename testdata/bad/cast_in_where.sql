-- expect: cast_in_where
SELECT id FROM events WHERE created_at::date = '2026-01-01';
