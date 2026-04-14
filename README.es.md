# pgopt — Optimizador y asesor de queries PostgreSQL

**[🇬🇧 Read in English](README.md)** | **[📖 Tutorial paso a paso (es)](docs/TUTORIAL.es.md)** · **[Tutorial (en)](docs/TUTORIAL.en.md)**

Herramienta 100% CLI que analiza queries SQL y recomienda optimizaciones basadas en el conocimiento profundo del motor de PostgreSQL (MVCC, planner, executor, índices, autovacuum). Cada hallazgo cita el archivo y línea exactos del código fuente de PostgreSQL donde vive la lógica relevante — podés verificar cualquier afirmación abriendo el archivo citado.

Construida sobre `pg_query_go` — **el parser real de PostgreSQL embebido** — así que ve los queries exactamente como los ve el servidor.

## Características

- **50 reglas** distribuidas en AST-only (31), schema-aware (10) y basadas en EXPLAIN (9).
- **Cada hallazgo referencia el código**: `src/backend/...:línea` en PostgreSQL.
- **Pipe-friendly**: stdin/stdout, exit codes, salida texto + JSON.
- **Segura por default**: conexión a DB corre en transacción `READ ONLY`; sin autofix.
- **Bien testeada**: unit tests por regla, fixtures de integración, race detector limpio.
- **Autocontenida**: cero servicios externos, solo Go + CGO.
- **Batteries included**: `docker compose up -d` levanta PostgreSQL 17 con schema sembrado para demos instantáneas.

## Arranque rápido

Requisitos: Go 1.22+, un toolchain de C (`pg_query_go` embebe el parser de PostgreSQL).

```sh
cd go_optimization
go mod tidy
make build          # produce ./pgopt

# Analizar un archivo
./pgopt query.sql

# Desde stdin (amigable para pipelines)
echo "SELECT * FROM users WHERE lower(email) = 'a@b.com' LIMIT 10 OFFSET 100000;" | ./pgopt -

# Listar todas las reglas
./pgopt --list-rules

# JSON para CI / pipelines
./pgopt --format json --fail-on warn query.sql
```

### Demo con base sembrada (requiere Docker)

```sh
# 1. Levantá PostgreSQL 17 con un schema realista ya cargado.
make docker-up

# 2. Apuntá pgopt a esa DB.
export PGOPT_DB="postgres://pgopt:pgopt@localhost:55432/pgopt?sslmode=disable"

# 3. Corré análisis schema-aware + basados en EXPLAIN contra datos reales.
./pgopt --db "$PGOPT_DB" --verbose testdata/demo/missing_idx.sql
./pgopt --db "$PGOPT_DB" --explain --verbose testdata/demo/bad_query_1.sql

# 4. Bajá el stack.
make docker-down
```

El seed (`docker/init.sql` + `docker/seed.sql`) crea **deliberadamente**:

- un índice duplicado (dispara `duplicate_index`)
- un índice redundante por prefijo (`redundant_index`)
- una FK sin índice de soporte (`fk_without_index`)
- una tabla sin primary key (`missing_primary_key`)
- una columna `timestamp` sin tz (`timestamp_without_tz`)
- una tabla `events` particionada por rango (`partition_key_unused`)
- una tabla grande de 500k filas para `explain_seqscan_large` / `explain_temp_buffers`.

## Ejemplo

```
$ ./pgopt --verbose testdata/bad/offset_big.sql

[WARN]  OFFSET 100000: cost is linear in the offset (executor produces and discards 100000 rows)
    [offset_pagination]  at 2:37
    ╰─ OFFSET 100000
    why: PostgreSQL's executor uses the Volcano pull model (src/include/executor/executor.h:
         ExecProcNode). OFFSET N literally calls the child node N times and throws the
         results away. Keyset pagination (WHERE key > :last_seen ORDER BY key LIMIT N)
         seeks directly in the index in O(log n).
    fix: Rewrite as: WHERE <order_col> > :last_value ORDER BY <order_col> LIMIT N.
         Track the last value seen on the client side between pages.
    ref: src/backend/executor/nodeLimit.c (OFFSET handling); GUIA_POSTGRES_ES_2.md §35.2

1 finding(s): 0 error, 1 warn, 0 info
```

## Reglas (50 integradas + JavaScript opcional)

> Las 50 reglas listadas abajo están compiladas en el binario y
> siempre disponibles. Activá `--js-rules` para cargar además reglas
> definidas por el usuario desde `rules-js/` (el repo trae 11 reglas
> JS de ejemplo). Ver [`docs/js-rules.md`](docs/js-rules.md).

### Sólo AST — 31 reglas (sin DB)

| Regla | Detecta |
|-------|---------|
| `select_star` | `SELECT *` — arruina Index-Only Scans, dispara TOAST detoast. |
| `offset_pagination` | `OFFSET N` con N grande. |
| `not_in_null` | `NOT IN (subquery)` — peligroso con NULLs; usar `NOT EXISTS`. |
| `cast_in_where` | `WHERE col::tipo = …` bloquea índices. |
| `function_on_column` | `WHERE lower(col) = …` etc. bloquea índices. |
| `like_leading_wildcard` | `WHERE col LIKE '%foo%'` — requiere pg_trgm + GIN. |
| `large_in_list` | `IN (v1…vN)` con ≥100 elementos. |
| `missing_where` | `UPDATE`/`DELETE` sin `WHERE`. |
| `not_sargable` | `WHERE col + N op X` — aritmética esconde la columna. |
| `select_for_update_no_limit` | `FOR UPDATE` sin `LIMIT`. |
| `distinct_on_joins` | `DISTINCT` + joins — huele a bug de cardinalidad. |
| `count_star_big` | `COUNT(*)` sobre tablas grandes — usar `pg_class.reltuples`. |
| `order_by_ordinal` | `ORDER BY 1, 2` — frágil. |
| `group_by_ordinal` | `GROUP BY 1` — frágil. |
| `implicit_cross_join` | `FROM a, b` sin predicado = Cartesiano. |
| `boolean_equals_true` | `col = true` es redundante. |
| `coalesce_in_where` | `COALESCE(col, …)` bloquea índices. |
| `order_by_random` | `ORDER BY random() LIMIT k` — escaneo completo. |
| `subquery_in_select` | Subquery correlacionada en SELECT = N+1. |
| `window_empty` | `OVER ()` sin PARTITION BY — probable accidente. |
| `truncate_in_transaction` | `TRUNCATE` en transacción bloquea ACCESS EXCLUSIVE. |
| `interval_on_indexed_column` | `col + interval ...` bloquea uso de índice. |
| `recursive_cte_no_limit` | `WITH RECURSIVE` sin guarda de terminación. |
| `union_vs_union_all` | `UNION` puede ocultar un `UNION ALL` más barato. |
| `insert_no_column_list` | `INSERT … VALUES` sin lista de columnas — frágil. |
| `delete_correlated_subquery` | `DELETE … WHERE EXISTS` → `DELETE … USING`. |
| `sum_case_when_count_filter` | `SUM(CASE WHEN …)` → `COUNT(*) FILTER`. |
| `having_without_group_by` | `HAVING` sin `GROUP BY`. |
| `is_null_in_where` | `IS NULL` hint para columnas con NULLs raros. |
| `cte_unused` | `WITH` declarado y nunca referenciado. |
| `vacuum_full_in_script` | `VACUUM FULL` desde código de app (rewrite bloqueante). |
| `in_subquery_readability` | `IN (SELECT …)` → sugerencia de `EXISTS`. |

### Schema-aware — 10 reglas (`--db`)

| Regla | Detecta |
|-------|---------|
| `missing_index` | Columna en WHERE sin índice btree líder. |
| `stale_stats` | Tabla nunca ANALYZEd o con alto ratio de tuplas muertas. |
| `partition_key_unused` | Tabla particionada consultada sin filtro por clave de partición. |
| `fk_without_index` | Columna FK sin índice de soporte. |
| `redundant_index` | Claves del índice son prefijo estricto de otro. |
| `duplicate_index` | Dos índices cubren idénticas columnas. |
| `unused_index` | `idx_scan == 0` — candidato a `DROP INDEX`. |
| `missing_primary_key` | Tabla regular sin PK. |
| `timestamp_without_tz` | Columna `timestamp` en vez de `timestamptz`. |

### Basadas en EXPLAIN — 9 reglas (`--db --explain`)

| Regla | Detecta |
|-------|---------|
| `explain_estimate_mismatch` | Estimación del planner vs real difiere >10×. |
| `explain_external_sort` | Sort spilleó a disco. |
| `explain_hash_batches` | Hash Join particionó a disco. |
| `explain_ios_heap_fetches` | Index-Only Scan cayó a heap (VM desactualizado). |
| `explain_seqscan_large` | Seq Scan sobre tabla grande con filtro muy selectivo. |
| `explain_nestloop_seq_inner` | Nested Loop con Seq Scan inner y muchos loops. |
| `explain_parallel_underused` | Menos workers lanzados que los planeados. |
| `explain_temp_buffers` | Nodo escribió archivos temporales. |
| `explain_cold_cache` | <50% cache hit ratio en el plan. |

## Referencia CLI

```
pgopt [flags] <archivo.sql | - >

--db URL               URL PostgreSQL (postgres://…) — habilita reglas schema/explain
--explain              Corre EXPLAIN (ANALYZE, BUFFERS) dentro de transacción READ ONLY
--rules SPEC           'all', 'r1,r2', o 'all,-r3' para excluir
--format FMT           text | json
--min-severity SEV     info | warn | error  (oculta lo menor)
--fail-on SEV          info | warn | error  (umbral de exit ≠0; default warn)
--color auto|always|never
--verbose              Incluye explicaciones y evidencia de código
--quiet                Suprime stdout; usa solo el código de salida
--list-rules           Muestra todas las reglas y sale
--js-rules             Habilita reglas de usuario en JavaScript
--js-rules-dir DIR     Carpeta escaneada para reglas JS (default: rules-js)
--ignore-file PATH     Archivo con IDs de reglas a omitir (default: .pgoptignore)
--no-ignore-file       Ignora el archivo aunque exista
--version              Imprime la versión de pgopt y sale
```

### Pragmas inline

Podés silenciar una regla para un query puntual sin tocar
`.pgoptignore` agregando un comentario SQL:

```sql
-- pgopt:ignore=select_star
SELECT * FROM intentionally_wide_view;

-- pgopt:ignore-next
SELECT *, calendar.day FROM calendar, regions;   -- producto cartesiano intencional
```

`-- pgopt:ignore=a,b,...` aplica al resto del archivo;
`-- pgopt:ignore-next` aplica a la primera línea no-vacía y no-comentario
siguiente.

**Códigos de salida**: 0 limpio, 1 hallazgos ≥ `--fail-on`, 2 error de parse, 3 error de config/IO.

### Archivo de ignore por proyecto

Dejá un `.pgoptignore` en la raíz del repo y `pgopt` lo toma automáticamente:

```
# .pgoptignore — silenciar reglas ruidosas en este proyecto
implicit_cross_join
boolean_equals_true
```

Un ID por línea; las líneas que empiezan con `#` son comentarios. Apuntá a otra ruta con `--ignore-file`, o desactivá el mecanismo con `--no-ignore-file`.

## Integración

### Pre-commit hook

```sh
#!/bin/sh
# .git/hooks/pre-commit
for f in $(git diff --cached --name-only --diff-filter=ACMR -- '*.sql'); do
  pgopt --fail-on error "$f" || exit 1
done
```

### CI

```yaml
# GitHub Actions
- run: |
    go install github.com/arturoeanton/postgres-go-optimization/cmd/pgopt@latest
    find . -name '*.sql' -exec pgopt --fail-on warn --format json {} +
```

### Asistentes de IA

Pasale la salida JSON como contexto — es chica, estructurada y cada item está anclado al código fuente del motor. Mucho mejor que un prompt "optimizá este query".

```sh
pgopt --format json --verbose query.sql > feedback.json
```

## Limitaciones conocidas

Hay un puñado de reglas AST que pueden dar falsos positivos sobre
idiomas que son legítimos en su contexto. Serán afinadas antes del
release estable 0.1.0:

| Regla | Cuándo dispara de más |
|-------|-----------------------|
| `implicit_cross_join` | Productos cartesianos intencionales (raros pero válidos, e.g. matriz calendario × regiones). |
| `boolean_equals_true` | Código generado por ORMs que siempre emite `col = TRUE` para mantener la forma del SQL consistente. |
| `is_null_in_where` | Chequeos legítimos de NULL en columnas sparse donde `IS NULL` es el camino más rápido. |

Si alguna de estas afecta un query legítimo en tu proyecto, agregá el
ID al `.pgoptignore` en la raíz del repo:

```
# .pgoptignore
implicit_cross_join
boolean_equals_true
```

Ver la [referencia CLI](#referencia-cli) más arriba para la semántica
completa del archivo de ignore.

## Arquitectura

```
cmd/pgopt/          — Entry point CLI + tests de CLI
internal/
  analyzer/         — AST walker, tipo Finding, orquestador, Context
  rules/            — Un archivo por regla; Register() en init()
  schema/           — Cargador del catálogo (pg_class, pg_index, pg_stat_user_tables)
  explain/          — Runner de EXPLAIN JSON + walker del plan
  rewriter/         — Motor de patches de texto usando info de localización de pg_query
  report/           — Renderers texto + JSON
testdata/
  bad/              — Fixtures que disparan reglas específicas
  good/             — Fixtures que deben producir cero hallazgos
integration_test.go — Ejecuta cada fixture end-to-end
```

### Agregar una regla

1. Crear `internal/rules/mi_regla.go`.
2. Implementar `rules.Rule` (ID, Description, DefaultSeverity, RequiresSchema, RequiresExplain, Check).
3. `Register()` en un `init()`.
4. Agregar `testdata/bad/mi_regla.sql` con header `-- expect: mi_regla`.
5. Agregar casos al matrix de `rules_test.go`.

## Tests

```sh
make test                    # todos los tests
go test -race ./...          # con race detector
go test -cover ./...         # con coverage
make fixtures                # recorre cada fixture bad visualmente
```

Coverage actual:
- `report`: 97%
- `analyzer`, `rewriter`: 95%
- `rules`: 90%
- `jsrules`: 86%
- `cmd/pgopt` (CLI): 82%
- `schema`, `explain`: cubiertos sólo por tests con DB viva

## Filosofía

- **No reinventar el planner.** El optimizer de PostgreSQL son 100k+ líneas de C maduro. Esta herramienta captura patrones que el optimizer *no puede* deshacer porque están horneados en la estructura del query, y aconseja sobre decisiones de schema/estadísticas/índices que le dan al optimizer insumos buenos.
- **Sin autofix por default.** Cada hallazgo tiene una sugerencia concreta pero el humano decide cuándo reescribir. Un futuro `--fix` puede aterrizar para el subset de transformaciones verdaderamente invariantes semánticamente.
- **Cada afirmación cita el código.** Si `pgopt` contradice el código de PostgreSQL, **gana el código** — mandá un PR para corregir la regla.

## Licencia

Estilo BSD, igual que el código de PostgreSQL referenciado a lo largo de la herramienta.
