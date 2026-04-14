-- docker/init.sql
--
-- Schema de demo que ejercita cada regla schema-aware de pgopt:
--  * users: tabla principal, con índices "buenos", un índice inútil y
--    columnas no indexadas para triggear missing_index.
--  * orders: FK a users SIN índice (triggerea foreign_key_without_index).
--  * categories: tabla chica sin primary key (triggerea missing_pk).
--  * audit_log: timestamp sin tz (triggerea timestamp_without_tz).
--  * events: tabla particionada por rango de fecha.
--  * big_table: tabla grande para tests de EXPLAIN (se llena en seed.sql).
--  * redundant_idx / duplicate_idx: para reglas redundant_index/duplicate_index.
--
-- Este archivo sólo define estructura; los datos se cargan en 02_seed.sql.

-- Extensiones usadas por la demo (pg_trgm, pg_stat_statements).
-- pg_stat_statements requiere shared_preload_libraries; cuando el script
-- se corre sin ese preload (por ejemplo, en CI contra una imagen de
-- postgres sin configuración adicional) lo ignoramos en lugar de abortar
-- el seed completo. docker-compose.yml ya lo preload-ea.
DO $ext$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'pg_stat_statements not available; skipping';
END
$ext$;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

--------------------------------------------------------------------
-- users
--------------------------------------------------------------------
CREATE TABLE users (
    id            bigserial PRIMARY KEY,
    email         text        NOT NULL,
    name          text        NOT NULL,
    country       text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    profile       jsonb
);

-- Indice unique en email (bueno).
CREATE UNIQUE INDEX users_email_idx ON users (email);

-- Indice multicolumna; se usa para range scans. La rule missing_index
-- debe IGNORAR queries que usen (country) o (country, created_at) porque
-- la leading column matchea.
CREATE INDEX users_country_created_idx ON users (country, created_at);

-- Indice redundante (prefijo estricto de users_country_created_idx).
-- Triggerea redundant_index.
CREATE INDEX users_country_idx ON users (country);

-- Indice duplicado: mismo conjunto de columnas que users_email_idx.
-- Triggerea duplicate_index.
CREATE INDEX users_email_dup_idx ON users (email);

-- NO creamos indice sobre `name` a propósito:
-- queries `WHERE name = …` deberían triggerear missing_index.

--------------------------------------------------------------------
-- orders: FK sin índice (anti-patrón).
--------------------------------------------------------------------
CREATE TABLE orders (
    id           bigserial PRIMARY KEY,
    user_id      bigint     NOT NULL REFERENCES users(id),  -- <-- sin índice propio
    status       text       NOT NULL,
    total_cents  integer    NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX orders_status_idx ON orders (status);

--------------------------------------------------------------------
-- categories: sin PK, triggerea missing_pk.
--------------------------------------------------------------------
CREATE TABLE categories (
    name        text NOT NULL,
    description text
);

--------------------------------------------------------------------
-- audit_log: timestamp SIN zona horaria (anti-patrón).
--------------------------------------------------------------------
CREATE TABLE audit_log (
    id        bigserial PRIMARY KEY,
    action    text      NOT NULL,
    occurred  timestamp NOT NULL,          -- <-- sin tz
    actor     text
);

--------------------------------------------------------------------
-- events: tabla particionada por rango de fecha.
--------------------------------------------------------------------
CREATE TABLE events (
    id      bigserial,
    ts      timestamptz NOT NULL,
    kind    text        NOT NULL,
    payload jsonb,
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);

CREATE TABLE events_2025_q4 PARTITION OF events
    FOR VALUES FROM ('2025-10-01') TO ('2026-01-01');
CREATE TABLE events_2026_q1 PARTITION OF events
    FOR VALUES FROM ('2026-01-01') TO ('2026-04-01');
CREATE TABLE events_2026_q2 PARTITION OF events
    FOR VALUES FROM ('2026-04-01') TO ('2026-07-01');

CREATE INDEX events_kind_idx ON events (kind);

--------------------------------------------------------------------
-- big_table: suficientemente grande como para que Seq Scan duela.
--------------------------------------------------------------------
CREATE TABLE big_table (
    id        bigserial PRIMARY KEY,
    -- columnas comunes
    status    text        NOT NULL,
    amount    numeric(12, 2) NOT NULL,
    label     text,
    -- Columna potencialmente TOASTeada
    payload   jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX big_table_status_idx ON big_table (status);

-- Vista que debería funcionar con Index-Only Scan si elegimos bien las columnas.
CREATE INDEX big_table_created_id_idx ON big_table (created_at) INCLUDE (id);
