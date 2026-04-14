# Tutorial paso a paso: usando pgopt para optimizar queries PostgreSQL

> Este tutorial asume que ya compilaste el binario (`make build` desde la raíz del repo o `go install ./cmd/pgopt`). Si tenés el archivo `./pgopt` listo, empezamos.

El objetivo: aprender a usar `pgopt` como una herramienta diaria — desde el prototipo en psql hasta un linter en CI que bloquea PRs con queries riesgosas.

**Cubrimos**: las 50 reglas integradas agrupadas en 3 capas (AST / schema / EXPLAIN), la capa opcional de **reglas propias en JavaScript**, cómo levantar la DB de demo con Docker, cómo agregar reglas nuevas (Go o JS), cómo integrar en CI y cómo darle contexto a una IA que escribe SQL.

---

## Lección 1 — El hello world

Arrancá con un query obviamente malo:

```sh
echo "SELECT * FROM users WHERE lower(email) = 'alice@example.com';" | ./pgopt -
```

Salida (resumida):

```
[WARN]  SELECT * pulls every column, defeating Index-Only Scans ...  [select_star]  at 1:8
[WARN]  Function `lower(email)` in WHERE disables plain indexes on `email`  [function_on_column]  at 1:28

2 finding(s): 0 error, 2 warn, 0 info
```

Dos warnings. El exit code es 1 porque tenemos warnings y `--fail-on` default es `warn`. Comprobalo:

```sh
echo "SELECT * FROM t" | ./pgopt -
echo "Exit: $?"
# Exit: 1
```

### Qué acaba de pasar

1. `pgopt` leyó el SQL de stdin.
2. Lo parseó con el parser real de PostgreSQL (vía `pg_query_go`).
3. Recorrió el AST aplicando 11 reglas sólo-AST.
4. Reportó dos hallazgos.

No se conectó a ninguna base, no generó estadísticas, no ejecutó nada. Puro análisis sintáctico + conocimiento del motor.

---

## Lección 2 — El modo verbose y el "por qué"

```sh
echo "SELECT * FROM t" | ./pgopt --verbose --color=always -
```

Ahora ves tres bloques por hallazgo:
- **`why:`** la explicación del motor. ¿Por qué es malo? Cita `src/backend/parser/parse_target.c` para que abras el archivo y verifiques.
- **`fix:`** la sugerencia concreta de reescritura.
- **`ref:`** la referencia canónica (archivo:línea) para profundizar.

Este modo es lo que le pasás a un humano que está aprendiendo, o a un asistente de IA para que entienda el contexto antes de reescribir.

---

## Lección 3 — Entrada desde archivo

```sh
cat > /tmp/q.sql <<'SQL'
SELECT id, email
  FROM users
 WHERE created_at::date = '2026-01-01'
 ORDER BY id
 LIMIT 50
 OFFSET 100000;
SQL

./pgopt --verbose /tmp/q.sql
```

Dos hallazgos:
- `cast_in_where` en la línea 3 (cast a `::date` rompe índice).
- `offset_pagination` en la línea 6 (offset grande).

**Cada hallazgo tiene una posición `line:col`** — podés hacer click en tu editor y saltar al punto exacto.

---

## Lección 4 — Seleccionar qué reglas correr

Tres formas:

```sh
# Todas (default)
./pgopt query.sql

# Subset específico
./pgopt --rules "select_star,offset_pagination" query.sql

# Todas menos una (útil cuando una regla tiene falso positivo en tu contexto)
./pgopt --rules "all,-distinct_on_joins" query.sql
```

Listá qué hay disponible:

```sh
./pgopt --list-rules
```

Las reglas marcadas `[schema]` o `[explain]` sólo corren con `--db`. Las marcadas `[js]` sólo aparecen cuando agregás `--js-rules` (ver Lección 12).

---

## Lección 5 — Control de severidad y exit codes

Tres niveles: `info`, `warn`, `error`.

```sh
# Ver sólo errores (ocultar warns e infos)
./pgopt --min-severity error query.sql

# No fallar en warns — sólo en errors (útil si querés solamente CI bloqueando "escape"s claros)
./pgopt --fail-on error query.sql

# Mostrar todo pero nunca fallar (solo informativo)
./pgopt --min-severity info --fail-on error query.sql
```

Códigos de salida:

| Código | Significado |
|--------|-------------|
| 0 | Sin hallazgos al nivel `--fail-on` o superior |
| 1 | Hay hallazgos al nivel `--fail-on` |
| 2 | El SQL no parsea (sintaxis inválida) |
| 3 | Error de configuración o I/O |

---

## Lección 6 — Salida JSON para scripts

```sh
./pgopt --format json query.sql
```

```json
{
  "findings": [
    {
      "rule": "select_star",
      "severity": 1,
      "message": "SELECT * pulls every column...",
      "explanation": "Expansion happens during parse_analyze...",
      "suggestion": "List only the columns you actually need...",
      "evidence": "src/backend/parser/parse_target.c:ExpandColumnRefStar",
      "location": {"start": 7, "end": 8},
      "snippet": "*"
    }
  ],
  "summary": { "total": 1, "error": 0, "warn": 1, "info": 0 }
}
```

Con esto podés:

```sh
# Contar warnings en tu carpeta de migraciones
find migrations -name '*.sql' -exec ./pgopt --format json --quiet {} \; \
  | jq -s '[.[] | .summary.warn] | add'

# Filtrar solo una regla
./pgopt --format json query.sql | jq '.findings[] | select(.rule=="offset_pagination")'
```

---

## Lección 7 — Conectarse a la base (schema-aware): Docker en 30 segundos

Las reglas schema-aware necesitan que `pgopt` lea el catálogo (`pg_class`, `pg_index`, `pg_stat_user_tables`, `pg_constraint`, etc.). Tenemos **un stack Docker incluido** que levanta PostgreSQL 17 con un schema sembrado a propósito con anti-patrones.

### Arrancar la DB de demo

```sh
make docker-up
# ✓ PostgreSQL ready on localhost:55432

export PGOPT_DB="postgres://pgopt:pgopt@localhost:55432/pgopt?sslmode=disable"
```

El seed (`docker/init.sql` + `docker/seed.sql`) crea:

| Tabla | Tamaño | Para qué |
|-------|--------|---------|
| `users` | 50k filas | Indice duplicado, índice redundante, columna sin índice. |
| `orders` | 200k filas | FK a `users.id` SIN índice propio. |
| `categories` | 50 filas | Sin PK. |
| `audit_log` | 20k filas | `occurred timestamp` (sin tz). |
| `events` | 150k filas | Particionada por rango de fecha. |
| `big_table` | 500k filas | Payload `jsonb` grande (TOAST), para tests de EXPLAIN. |

### 10 reglas schema-aware disparadas contra esta DB

```sh
echo "SELECT * FROM users u JOIN orders o ON o.user_id = u.id WHERE u.name = 'x';" \
  | ./pgopt --db "$PGOPT_DB" -
```

Salida real:

```
[WARN]  Duplicate indexes on users: `users_email_idx` and `users_email_dup_idx` cover identical columns
        [duplicate_index]
[WARN]  Foreign key `public.orders(user_id)` has no supporting index  [fk_without_index]
[WARN]  No index on `public.users(name)` for predicate `=`  [missing_index]
[INFO]  Index `users_country_idx` on users is a strict prefix of `users_country_created_idx`
        and is likely redundant  [redundant_index]
```

Querés chequear cada regla por separado:

```sh
echo "SELECT * FROM categories;" | ./pgopt --db "$PGOPT_DB" --rules missing_primary_key -
echo "SELECT * FROM audit_log;" | ./pgopt --db "$PGOPT_DB" --rules timestamp_without_tz -
./pgopt --db "$PGOPT_DB" --rules partition_key_unused testdata/demo/partition_unused.sql
echo "SELECT * FROM big_table LIMIT 1;" | ./pgopt --db "$PGOPT_DB" --rules unused_index -
```

**Seguridad**: la conexión se pone en `SET default_transaction_read_only = on` apenas se abre. No hay forma de que `pgopt` modifique tu DB, **ni siquiera con `--explain`** (que corre dentro de `BEGIN READ ONLY`).

### Bajar el stack

```sh
make docker-down
```

---

## Lección 8 — EXPLAIN integrado: 9 reglas sobre el plan real

```sh
./pgopt --db "$PGOPT_DB" --explain --verbose testdata/demo/bad_query_1.sql
```

Esto:

1. Conecta a la DB.
2. Abre transacción `READ ONLY` (protección: un `EXPLAIN ANALYZE` sobre un `INSERT`/`UPDATE`/`DELETE` NO ejecuta las modificaciones).
3. Corre `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)`.
4. Hace `ROLLBACK`.
5. Parsea el plan JSON.
6. Aplica **9 reglas** basadas en el plan real.

Lo que detecta:

| Regla | Pista operativa |
|-------|-----------------|
| `explain_estimate_mismatch` | `rows` estimadas vs reales difieren >10× → `ANALYZE`, `SET STATISTICS`, `CREATE STATISTICS`. |
| `explain_external_sort` | Sort spilleó a disco → `SET LOCAL work_mem`. |
| `explain_hash_batches` | Hash Join particionó a disco → mismo remedio. |
| `explain_ios_heap_fetches` | Index Only Scan cayó a heap (VM desactualizado) → `VACUUM`. |
| `explain_seqscan_large` | Seq Scan sobre tabla grande con filtro muy selectivo → índice candidato. |
| `explain_nestloop_seq_inner` | Nested Loop con Seq Scan inner + muchos loops → índice en inner o forzar hash/merge. |
| `explain_parallel_underused` | Menos workers lanzados que planeados → subir `max_parallel_workers`. |
| `explain_temp_buffers` | Nodo escribió archivos temporales → `SET LOCAL work_mem`. |
| `explain_cold_cache` | <50% cache hit → cold start o `shared_buffers` chico. |

Este modo es **caro** (ejecuta la query). Úsalo en CI solo para queries críticas, o manualmente cuando estás investigando performance.

### Demo contra la DB sembrada

Con `make docker-up` corriendo:

```sh
./pgopt --db "$PGOPT_DB" --explain --verbose testdata/demo/bad_query_1.sql
```

La query demo mezcla 5 anti-patrones estructurales y además produce (sobre los datos sembrados) un nested loop caro y hallazgos del plan. Vas a ver ~10 findings de las 3 capas al mismo tiempo — un buen ejemplo de por qué conviene correr pgopt en modo full.

---

## Lección 9 — Integración con humanos y asistentes que escriben SQL

Si estás asistiendo a un desarrollador o a un modelo de lenguaje que escribe SQL, el patrón es:

```sh
pgopt --format json --verbose query.sql > feedback.json
```

El JSON tiene:
- El problema (message)
- **Por qué** es problema (explanation con cita al código)
- **Qué hacer** (suggestion)

Pasale ese JSON al modelo o al dev como contexto. Vas a ver que los rewrites son mucho mejores que con un prompt vacío "optimizá este query".

### Workflow típico

1. Dev escribe query.
2. `pgopt --format json query.sql` produce feedback.
3. El asistente recibe `query.sql` + `feedback.json` y propone una versión optimizada.
4. Dev corre `pgopt` sobre la versión nueva — idealmente sin hallazgos.
5. Tests de la app pasan — merge.

---

## Lección 10 — Integración en CI/CD

### GitHub Actions

```yaml
name: SQL lint

on: [pull_request]

jobs:
  pgopt:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.22' }
      - run: go install github.com/arturoeanton/postgres-go-optimization/cmd/pgopt@latest
      - name: Lint SQL
        run: |
          changed=$(git diff --name-only origin/${{ github.base_ref }}..HEAD -- '*.sql')
          for f in $changed; do
            echo "::group::$f"
            pgopt --fail-on warn --verbose "$f"
            echo "::endgroup::"
          done
```

### GitLab CI

```yaml
pgopt:
  image: golang:1.22
  script:
    - go install github.com/arturoeanton/postgres-go-optimization/cmd/pgopt@latest
    - find . -name '*.sql' -exec pgopt --fail-on warn {} +
```

### Pre-commit hook local

```sh
cat > .git/hooks/pre-commit <<'EOF'
#!/bin/sh
for f in $(git diff --cached --name-only --diff-filter=ACMR -- '*.sql'); do
  pgopt --fail-on error "$f" || exit 1
done
EOF
chmod +x .git/hooks/pre-commit
```

---

## Lección 11 — Agregar una regla en Go

Escenario: querés detectar `WHERE col = ANY(ARRAY[...])` con arrays enormes, porque en tu shop notaron que ese patrón genera planes malos.

### Paso 1 — Escribir la regla

Creá `internal/rules/my_array_any.go`:

```go
package rules

import (
    "fmt"
    "github.com/arturoeanton/postgres-go-optimization/internal/analyzer"
)

type largeArrayAny struct{}

func (largeArrayAny) ID() string                         { return "large_array_any" }
func (largeArrayAny) Description() string                { return "= ANY(ARRAY[…]) con array enorme: preferí JOIN con VALUES" }
func (largeArrayAny) DefaultSeverity() analyzer.Severity { return analyzer.SeverityWarn }
func (largeArrayAny) RequiresSchema() bool               { return false }
func (largeArrayAny) RequiresExplain() bool              { return false }

func (largeArrayAny) Check(ctx *analyzer.Context) []analyzer.Finding {
    const threshold = 100
    var out []analyzer.Finding
    analyzer.Walk(ctx.AST, func(_ []string, n analyzer.ASTNode) bool {
        if analyzer.NodeKind(n) != "A_ArrayExpr" {
            return true
        }
        elems := analyzer.AsList(analyzer.Inner(n), "elements")
        if len(elems) < threshold {
            return true
        }
        loc := analyzer.AsInt(analyzer.Inner(n), "location")
        out = append(out, analyzer.Finding{
            Severity:   analyzer.SeverityWarn,
            Message:    fmt.Sprintf("ARRAY con %d elementos; preferí JOIN con VALUES o temp table", len(elems)),
            Suggestion: "Reemplazá `= ANY(ARRAY[…])` con `JOIN (VALUES (…),(…)) v(col) USING(col)`.",
            Evidence:   "GUIA_POSTGRES_ES_2.md §35.4",
            Location:   analyzer.Range{Start: loc, End: loc + 1},
        })
        return true
    })
    return out
}

func init() { Register(largeArrayAny{}) }
```

### Paso 2 — Fixture de test

```sh
cat > testdata/bad/large_array_any.sql <<'SQL'
-- expect: large_array_any
SELECT id FROM users WHERE id = ANY(ARRAY[1,2,3, …,150]);
SQL
```

### Paso 3 — Agregar casos al test matrix

En `internal/rules/rules_test.go`, añadí:

```go
"large_array_any": {
    {"big_array", "SELECT 1 FROM t WHERE id = ANY(ARRAY[" + repeatInt(150) + "])", true},
    {"small_array", "SELECT 1 FROM t WHERE id = ANY(ARRAY[1,2,3])", false},
},
```

### Paso 4 — Correr tests

```sh
go test ./...
```

Si todo pasa, la regla aparece ya en `./pgopt --list-rules`.

---

## Lección 12 — Reglas propias en JavaScript

Cuando querés encapsular convenciones internas de tu proyecto sin forkear el binario, podés escribir reglas en JavaScript y cargarlas en runtime. Son **opt-in**: sin `--js-rules` no se tocan, y no pagás costo de startup.

### Un vistazo rápido

```sh
./pgopt --js-rules --js-rules-dir ./rules-js query.sql
./pgopt --js-rules --list-rules   # ahora las reglas JS aparecen con [js]
```

El repositorio trae 11 reglas de ejemplo en `rules-js/`. Mirá `rules-js/js_count_literal/` como plantilla mínima.

### Layout de una regla

```
rules-js/
    mi_regla/
        manifest.json
        main.js
```

`manifest.json`:

```json
{
  "id": "mi_regla",
  "description": "Descripción de una línea, presente simple.",
  "severity": "warn",
  "requiresSchema": false,
  "requiresExplain": false,
  "evidence": "src/backend/.../archivo.c"
}
```

`main.js` debe exportar una función `check(ctx)`:

```js
function check(ctx) {
    const out = [];
    pg.forEachSelect(ctx.ast, function (sel) {
        // lógica
    });
    return out;
}
```

El contexto que recibe tu regla:

- `ctx.ast` — árbol JSON del parser real de PostgreSQL (mismo shape que ven las reglas Go).
- `ctx.source` — el SQL original.
- `ctx.schema` — `null` a menos que el manifest marque `requiresSchema`.
- `ctx.explain` — `null` a menos que el manifest marque `requiresExplain`.

### La API `pg.*`

El trabajo pesado está implementado en Go y expuesto como funciones globales sobre `pg`. Usalas en vez de traversar el árbol a mano en JS — son ~10× a 50× más rápidas.

| Función | Para qué |
|---------|----------|
| `pg.walk(tree, visit)` | Recorrido DFS. `visit(path, node)` devuelve `false` para podar. |
| `pg.forEachStmt(tree, fn)` | Itera sentencias top-level: `fn(kind, inner, wrapper)`. |
| `pg.forEachSelect(tree, fn)` | Visita todo `SelectStmt` (incluso anidados). |
| `pg.findNodes(tree, kind)` | Devuelve todos los inner maps con ese wrapper. |
| `pg.firstLocation(node)` | Primer offset byte en el subárbol. |
| `pg.nodeKind(n)` / `pg.inner(n)` | Key único del wrapper / inner map. |
| `pg.asString/asInt/asList/asMap(n, key)` | Accesores seguros. |
| `pg.columnRefs(tree)` / `pg.tableRefs(tree)` | Extracción común de columnas y tablas. |
| `pg.finding(msg, sug?, locStart?, locEnd?)` | Builder de hallazgo para el caso común. |

### Ejemplo completo

```js
// rules-js/js_count_literal/main.js
function check(ctx) {
    const out = [];
    pg.findNodes(ctx.ast, "FuncCall").forEach(function (fc) {
        const nameList = pg.asList(fc, "funcname") || [];
        if (nameList.length === 0) return;
        const last = nameList[nameList.length - 1];
        if (pg.asString(pg.asMap(last, "String"), "sval").toLowerCase() !== "count") return;
        if (fc.agg_star) return;
        const args = pg.asList(fc, "args") || [];
        if (args.length !== 1 || pg.nodeKind(args[0]) !== "A_Const") return;
        const loc = pg.firstLocation(fc);
        out.push(pg.finding(
            "COUNT(<literal>) es idéntico a COUNT(*); preferí COUNT(*).",
            "Reemplazá COUNT(1) por COUNT(*).",
            loc, loc + 1
        ));
    });
    return out;
}
```

### Testeo

Poné una fixture en `testdata/bad-js/` con el header de expectativa y corré `go test ./...`:

```sql
-- expect: mi_regla
SELECT COUNT(1) FROM users;
```

El runner integrado carga `rules-js/`, ejecuta la unión Go+JS, y verifica que tu regla dispare sobre la fixture.

### Manejo de errores

Si tu `check()` tira una excepción, `pgopt` la captura y la reporta como un hallazgo `info` con el mensaje de error — el análisis del resto de reglas sigue. Esto evita que una regla nueva rompa el build en silencio.

### Ver `docs/js-rules.md` para la referencia completa de la API.

---

## Lección 13 — Visión panorámica: las capas

Para tener el inventario a mano mientras aprendés:

```sh
./pgopt --list-rules               # solo Go (50)
./pgopt --js-rules --list-rules    # incluye las JS ([js])
```

**Estructura:**

```
 ┌───────────────────────────────────────────────────────────┐
 │  Capa AST (31 reglas)  — se corren SIN DB                 │
 │  Rápido (ms), sin efectos colaterales                     │
 │  Patrones: select *, offset grande, cast, función en col, │
 │  like con %, not in, order by random, subquery en select, │
 │  recursive cte sin limit, vacuum full, truncate en tx…    │
 └───────────────────────────────────────────────────────────┘
                          ↓ (si --db)
 ┌───────────────────────────────────────────────────────────┐
 │  Capa Schema-aware (10 reglas) — lee pg_class, pg_index…  │
 │  Solo SELECTs; READ ONLY; seguro                          │
 │  Patrones: missing_index, fk_without_index, duplicate_*,  │
 │  redundant_index, unused_index, missing_primary_key,      │
 │  stale_stats, partition_key_unused, timestamp_without_tz  │
 └───────────────────────────────────────────────────────────┘
                          ↓ (si --explain)
 ┌───────────────────────────────────────────────────────────┐
 │  Capa EXPLAIN (9 reglas) — corre EXPLAIN ANALYZE real     │
 │  Caro (ejecuta la query), READ ONLY                       │
 │  Patrones: estimate_mismatch, external_sort, hash_batches,│
 │  ios_heap_fetches, seqscan_large, nestloop_seq_inner,     │
 │  parallel_underused, temp_buffers, cold_cache             │
 └───────────────────────────────────────────────────────────┘
                  ⊕ (si --js-rules, ortogonal)
 ┌───────────────────────────────────────────────────────────┐
 │  Capa JavaScript (N reglas definidas por el proyecto)     │
 │  Opt-in; convivencia con cualquiera de las otras capas    │
 │  Ideal para convenciones internas y reglas de dominio     │
 └───────────────────────────────────────────────────────────┘
```

Regla que sigo personalmente:
- **Commit / PR**: capa AST obligatoria (rápida, detecta 90% de los problemas).
- **Pre-deploy**: + capa schema-aware contra staging.
- **Sospecha de performance**: + capa EXPLAIN sobre la query concreta.
- **Convenciones del equipo**: + `--js-rules` en los repos donde tengas reglas internas.

---

## Lección 14 — Diagnóstico de un query real, end to end

```sql
-- dashboard.sql
SELECT u.*, o.total_cents
  FROM users u
  JOIN orders o ON o.user_id = u.id
 WHERE lower(u.email) LIKE '%@example.com%'
   AND o.created_at::date = '2026-01-01'
 ORDER BY u.id
 LIMIT 50 OFFSET 5000;
```

### Paso 1: análisis offline

```sh
./pgopt --verbose dashboard.sql
```

Vas a ver:

- `select_star` en `u.*`.
- `function_on_column` en `lower(u.email)`.
- `like_leading_wildcard` en `'%@example.com%'`.
- `cast_in_where` en `o.created_at::date`.
- `offset_pagination` en `OFFSET 5000`.

**Cinco** anti-patrones en un solo query. Cada uno, si lo arreglás, puede dar 10–1000× de mejora.

### Paso 2: reescribir guiado por las sugerencias

```sql
SELECT u.id, u.email, u.name, o.total_cents
  FROM users u
  JOIN orders o ON o.user_id = u.id
 WHERE u.email LIKE '%@example.com'                          -- sin % inicial; btree sirve
   AND o.created_at >= '2026-01-01'
   AND o.created_at <  '2026-01-02'                          -- rango en vez de ::date
 ORDER BY u.id
 LIMIT 50;                                                   -- sin OFFSET
-- la app guarda el último u.id visto y filtra WHERE u.id > :last_id en la próxima página
```

### Paso 3: verificar

```sh
./pgopt --verbose dashboard_v2.sql
# ✓ no findings — query looks clean
```

### Paso 4: con DB, chequear plan

```sh
./pgopt --db "$DATABASE_URL" --explain dashboard_v2.sql
```

Si ves `explain_estimate_mismatch`, arreglá estadísticas. Si ves `explain_seqscan_large`, creá el índice que falta. Si todo limpio → deploy.

---

## Resumen: el flujo diario

```
 [escribís SQL]
      │
      ▼
 pgopt offline  ──────► hallazgos AST (rápido, local)
      │
      │ sin hallazgos
      ▼
 pgopt --db     ──────► hallazgos schema-aware
      │
      │ sin hallazgos
      ▼
 pgopt --explain ─────► hallazgos del plan real (caro, definitivo)
      │
      │ sin hallazgos
      ▼
 deploy con confianza
```

Cada paso es barato; escalás solo cuando el anterior no encontró nada. La heurística es: **90% de los problemas se atrapan en offline. 9% con `--db`. 1% requieren `--explain`**.

Las reglas JS son ortogonales: podés activarlas en cualquier paso del flujo con `--js-rules`.

---

## Lección 15 — Trucos y FAQs

### ¿Puedo correrlo sobre un archivo con múltiples sentencias?

Sí. `pgopt` parsea todo el contenido como una lista de `stmts`. Cada regla ve cada sentencia. Es útil para archivos de migración.

```sh
./pgopt migration_042.sql
```

### ¿Puedo deshabilitar una regla de por vida?

Por proyecto, con un wrapper shell o con un `.pgoptrc` que aún no existe (roadmap). Hoy la forma es:

```sh
alias pgopt='pgopt --rules "all,-is_null_in_where,-union_vs_union_all"'
```

### ¿Cómo integrar con `golangci-lint` o `sqlfluff`?

No se pisa con ellos: `sqlfluff` es de estilo/formato; `golangci-lint` es Go. `pgopt` es específico de **semántica PostgreSQL**. Lo común es: sqlfluff primero (format/style), pgopt después (engine-specific).

### El parser rechaza mi SQL válido

`pg_query_go` embebe el parser real de PostgreSQL 17. Si tu SQL usa sintaxis de Oracle/MSSQL/MySQL, no va a parsear. Ejemplos: `TOP N`, `NOLOCK`, `ROWNUM`. Eso es correcto: no es SQL válido para PostgreSQL.

### `--explain` tarda mucho

`EXPLAIN ANALYZE` ejecuta la query. Si la query tarda 30 segundos, el análisis tarda 30 segundos. Opciones:
- Reducir el alcance de la query (agregar `LIMIT` antes de analizar).
- Correr en staging con datos representativos pero menores.
- Usar `EXPLAIN` sin `ANALYZE` (pero perdés las comparaciones real-vs-estimado — **no vale la pena**).

### Mis reglas JS no aparecen en `--list-rules`

Necesitás pasar `--js-rules`. Sin ese flag `pgopt` ni siquiera abre el directorio — es intencional, para garantizar cero costo cuando no se usan.

### Una regla JS rompe el análisis

Los errores de runtime en JS se convierten en hallazgos `info` con el mensaje de la excepción. El resto de reglas sigue corriendo. Mirá el output por un hallazgo con tu rule id para ver el stack.

---

## Para seguir aprendiendo

- `./pgopt --list-rules` — todas las reglas con descripción.
- `./pgopt --verbose` — siempre ver explicaciones y referencias al código.
- `docs/js-rules.md` — referencia completa de la API para reglas JS.
- `docker/init.sql` y `docker/seed.sql` — el schema de demo comentado con cada anti-patrón.
- Las reglas citan archivos del árbol fuente de PostgreSQL (`src/backend/...`), navegable en <https://github.com/postgres/postgres>. Cuando aparece `GUIA_POSTGRES_ES_2.md §N.N` se refiere a una guía compañera del motor (material externo); ese enlace quedará publicado junto con el release estable.

Si una regla te molesta o te faltan chequeos, abrí una issue o mandá un PR — este proyecto es diseñado para crecer con casos reales.

— Fin del tutorial —
