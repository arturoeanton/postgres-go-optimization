-- expect: not_sargable
SELECT id FROM orders WHERE total_cents + 100 > 10000;
