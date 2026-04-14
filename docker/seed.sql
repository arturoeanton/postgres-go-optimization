-- docker/seed.sql
--
-- Pobla las tablas con datos suficientes para que las estimaciones del
-- planner tengan sentido, y para que las reglas basadas en EXPLAIN
-- encuentren cosas interesantes. El seed es determinístico.

--------------------------------------------------------------------
-- users (50k)
--------------------------------------------------------------------
INSERT INTO users (email, name, country, created_at, last_login_at, profile)
SELECT
    'u' || i || '@example.com',
    'User ' || i,
    (ARRAY['AR','CL','UY','BR','US','MX','ES'])[1 + (i % 7)],
    now() - ((i % 365) || ' days')::interval,
    CASE WHEN i % 5 = 0 THEN NULL
         ELSE now() - ((i % 30) || ' days')::interval END,
    jsonb_build_object('age', 18 + (i % 60), 'tier', (ARRAY['free','pro','team'])[1 + (i % 3)])
FROM generate_series(1, 50000) AS s(i);

--------------------------------------------------------------------
-- orders (200k)
--------------------------------------------------------------------
INSERT INTO orders (user_id, status, total_cents, created_at)
SELECT
    1 + (i % 50000),
    (ARRAY['pending','paid','shipped','cancelled','refunded'])[1 + (i % 5)],
    (1000 + (i * 37) % 50000),
    now() - ((i % 180) || ' days')::interval
FROM generate_series(1, 200000) AS s(i);

--------------------------------------------------------------------
-- categories (50 filas, sin PK)
--------------------------------------------------------------------
INSERT INTO categories (name, description)
SELECT 'cat-' || i, 'Description for category ' || i
FROM generate_series(1, 50) AS s(i);

--------------------------------------------------------------------
-- audit_log (20k)
--------------------------------------------------------------------
INSERT INTO audit_log (action, occurred, actor)
SELECT
    (ARRAY['login','logout','update','create','delete'])[1 + (i % 5)],
    (now() - ((i % 90) || ' days')::interval)::timestamp,
    'user' || (1 + (i % 1000))
FROM generate_series(1, 20000) AS s(i);

--------------------------------------------------------------------
-- events (particionada; 150k repartidas)
--------------------------------------------------------------------
INSERT INTO events (ts, kind, payload)
SELECT
    TIMESTAMPTZ '2025-10-01' + ((i * 83) % 272) * interval '1 day'
                              + ((i % 86400) || ' seconds')::interval,
    (ARRAY['click','view','purchase','signup'])[1 + (i % 4)],
    jsonb_build_object('seq', i)
FROM generate_series(1, 150000) AS s(i);

--------------------------------------------------------------------
-- big_table (500k) — payload grande para TOAST/Seq Scan visibles
--------------------------------------------------------------------
INSERT INTO big_table (status, amount, label, payload, created_at)
SELECT
    CASE WHEN i % 100 < 5 THEN 'hot'   -- 5% "hot" (muy selectivo)
         WHEN i % 100 < 40 THEN 'warm'
         ELSE 'cold' END,
    (i * 13)::numeric / 100,
    'label-' || (i % 5000),
    jsonb_build_object('seq', i, 'blob', repeat('x', 200 + (i % 1000))),
    now() - ((i % 730) || ' days')::interval
FROM generate_series(1, 500000) AS s(i);

--------------------------------------------------------------------
-- Tras cargar, actualizar estadísticas.
--------------------------------------------------------------------
ANALYZE users;
ANALYZE orders;
ANALYZE categories;
ANALYZE audit_log;
ANALYZE events;
ANALYZE big_table;
