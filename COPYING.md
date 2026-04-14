# Licensing

## This project: GPL-3.0-or-later

`pgopt` (the tool, its source code, and its documentation) is licensed under the GNU General Public License version 3, or (at your option) any later version. The full text is in [`LICENSE`](LICENSE).

This means:

- You may **use** `pgopt` freely, including in commercial settings.
- You may **modify** `pgopt` and distribute your modifications.
- If you **distribute** `pgopt` (modified or not, as a binary or as source), you must:
  - Include the full GPLv3 text.
  - Provide the corresponding source code (or a written offer to provide it).
  - License your modifications under GPLv3 as well.
- **Using `pgopt` to analyze your SQL does not impose any license on your SQL or on your application.** The GPL's copyleft applies to derivative works of `pgopt`'s source code, not to the data you feed into the tool.

## Dependencies and their licenses

| Dependency | License | Role |
|------------|---------|------|
| `github.com/pganalyze/pg_query_go/v6` | BSD-3-Clause (bundles PostgreSQL parser under PostgreSQL License) | Parses SQL into AST |
| `github.com/jackc/pgx/v5` | MIT | PostgreSQL driver |
| `github.com/jackc/pgpassfile` | MIT | pgx transitive |
| `github.com/jackc/pgservicefile` | MIT | pgx transitive |
| `golang.org/x/crypto` | BSD-3-Clause | pgx transitive |
| `golang.org/x/text` | BSD-3-Clause | pgx transitive |
| `google.golang.org/protobuf` | BSD-3-Clause | pg_query transitive |

All of these are GPLv3-compatible. Redistributing `pgopt` as a single binary is compliant with each of their terms.

## About PostgreSQL citations

`pgopt`'s rules cite specific files and line numbers in the PostgreSQL source tree (for example, `src/backend/optimizer/path/indxpath.c`). These citations are short factual references (fair use). They do not incorporate PostgreSQL source code into `pgopt` and do not impose the PostgreSQL License on this project.

## About findings and reports

Findings emitted by `pgopt` (text or JSON) are produced from the SQL you provide as input. They are your output. `pgopt`'s license does not extend to them.

## If you want to use parts of `pgopt` in a non-GPL project

Open an issue or contact the maintainer. Relicensing of specific parts (e.g., the rule interface shape) to permissive terms is negotiable on a case-by-case basis; most of the tool needs to remain GPL because the dependency graph is compatible and the copyleft is intentional.
