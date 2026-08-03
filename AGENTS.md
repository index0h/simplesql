# AGENTS.md

This file provides guidance to AI coding agents when working with code in this repository.

## Project

`simplesql` — a CLI code generator that reads Go repository interfaces, extracts SQL from method comments (using a
custom template syntax), and generates implementations that build queries and scan results via `database/sql`.

## Commands

```bash
# Build the CLI
go build ./cmd/simplesql

# Run the generator
./simplesql path/to/simplesql.yaml

# Run all tests
go test ./...

# Run tests with race detector
go test -race ./...

# Run linter
go tool golangci-lint run ./...

# Functional tests (spin up real MySQL/Postgres via Docker; SQLite runs in-process)
docker compose up -d
go test -tags functional ./examples/...
docker compose down
```

## CI

`.github/workflows/ci.yml` runs on every push to `main` and every PR: `build-and-test` (build, vet, `go test -race
./...`) and `lint` (`go tool golangci-lint run --build-tags functional ./...`, covering both build configs in one
pass) run in parallel; `functional-test` waits on both, then brings up `docker-compose.yml` with
`docker compose up -d --wait` and runs the functional suite.

## Functional tests

`examples/{mysql,postgres,sqlite}/` each hold a copy of the same `UserRepository` interface, its generated
`repository_gen.go`, and a `//go:build functional` test file that runs the generated code against a real database —
covering every return kind, bound params, direct substitution, nested `{{if}}`/`{{else if}}`, the no-context path,
transaction commit/rollback via `ConnectionManager`, and joins scanned into a struct that embeds multiple entities
(`UserBalance struct { User; Balance }`, via `FindWithBalance`/`ListWithBalances`). `docker-compose.yml` at the repo
root provisions MySQL and Postgres (schemas auto-applied from `examples/schema/*.sql` via
`docker-entrypoint-initdb.d`); SQLite needs no container. These tests are excluded from `go test ./...` by the build
tag, since they require the containers to be running. After editing `examples/*/repository.go`, regenerate with
`go run ./cmd/simplesql examples/<dialect>/config.yaml`.

`simplesql.FieldPointers`/`ScanRows` (`simplesql/scan.go`) flatten anonymous embedded struct fields recursively when
building the column → field index, so a join's result struct can embed each joined entity directly instead of
needing one flat struct with all columns redeclared. See `simplesql/scan_test.go` for the reflection-level unit tests.

Postgres has no `LastInsertId()` equivalent (`pgx`/`lib/pq` both error on it — there's no wire-protocol call for it,
only a `RETURNING` clause), so an `INSERT` method with a `(int, error)` return compiles differently under
`dialect: postgres`: `Compiler.CompileBody` appends `RETURNING id` to the query (skipped if the template already has
its own `RETURNING`, to avoid a doubled clause), and the generator pairs that with `insertIDReturningTmpl`
(`QueryRowContext` + `Scan`) instead of `insertIDTmpl` (`ExecContext` + `LastInsertId`) — selected via
`Compiler.Dialect()`, which `generator.go`'s `renderMethod` checks for the `QueryInsert`+`ReturnRowsOrID` case. This
assumes the primary key column is named `id`; there's no per-method way to configure a different column name.

## Package structure

```
cmd/simplesql/       CLI entrypoint — loads config, calls parser + generator
internal/config/     YAML config loading (dialect, repositories map) via go.uber.org/config
db/parser/           Go AST parser — reads interfaces and extracts method comments as SQL
db/generator/        Code generator — renders Go source from parsed interfaces
db/simplesql/          Public package: Querier interface + ScanRows/FieldPointers used by generated code
```

`internal/config` is only used by the CLI. Generated code imports only `simplesql`.

## Architecture

All three main services are stateless structs with constructor functions:

- `parser.Parser` — constructed with `NewParser(log)`, entry point is `ParseInterface(dir, ifaceName)`
- `generator.Compiler` — constructed with `NewCompiler(dialect)`, entry point is `CompileBody(method)`
- `generator.Generator` — constructed with `NewGenerator(dialect, log)`, entry point is
  `Generate(packageName, requests)`
- `generator.Tokenizer` — constructed with `NewTokenizer()`, used internally by `Compiler`

Logging uses `go.uber.org/zap` (passed into constructors). Config uses `go.uber.org/config`.

## How generation works

1. Config YAML maps `dir/InterfaceName → output_file.go`
2. `Parser.ParseInterface` reads all `.go` files in the given directory, finds the named interface via AST
3. For each method: its doc comment is scanned for an `@sql` tag. Everything before `@sql` is a regular comment and
   is ignored; everything from `@sql` (exclusive) to the end of the comment block is the query template. Methods
   without an `@sql` tag are skipped.
4. `Generator.Generate` emits a `_gen.go` file with a struct implementing the interface; the struct is named by
   lowercasing the first letter of the interface name and appending `Impl` (e.g. `UserRepository` →
   `userRepositoryImpl`)

## SQL template conventions

- `@sql` — marks the start of the query template within a method's doc comment; text before it is a plain comment
  and is not part of the generated query
- `:paramName` — bound parameter, rewritten to `?` (MySQL/SQLite) or `$N` (Postgres)
- `{{paramName}}` — direct string substitution into the query (param must be `string` or `*string`)
- `{{if <go-expr>}}...{{else if <go-expr>}}...{{else}}...{{end}}` — conditional block; the expression is emitted
  verbatim into the generated `if` statement

The template uses a custom lexer (`db/generator/tokenizer.go`) — not `text/template`. Expressions in `{{if ...}}` are
passed straight through to Go, so full Go syntax works: `enabled != nil`, `count > 0`, `enabled != nil && *enabled`,
etc.

## Return type mapping

| Signature       | Query kind    | Behavior                |
|-----------------|---------------|-------------------------|
| `(*T, error)`                | SELECT        | `nil, nil` if not found |
| `([]*T, error)` / `([]T, error)` | SELECT     | empty slice if none     |
| `(int, error)`                | INSERT        | last insert ID          |
| `(int, error)`                | UPDATE/DELETE | rows affected           |
| `(error)`                     | any           | error only              |
| `()`                          | any           | nothing                 |

`ReturnSlice` accepts both a slice of pointers (`[]*T`) and a slice of values (`[]T`) — `Method.ReturnIsPtr` (set in
`parser.go`'s `buildReturn`) records which one the interface declared, and `selectManyTmpl` in `templates.go` renders
`[]*T`/`[]T` accordingly (`{{if .ReturnIsPtr}}*{{end}}`); `simplesql.ScanRows` already handled both variants via
reflection before this, it's only the parser/template that were pointer-only. `ReturnSingle` (`*T`) has no such value
counterpart — it's pointer-only, since there'd be no sensible zero value to return for "no row found".

Any other param or return signature is a parse error, not a silent fallback — e.g. a non-pointer single return
(`(User, error)`), a second return value that isn't `error`, a param/return type `buildReturn`/`buildParams` can't
stringify (`interface{}`, `...T`, func/channel/generic types — the `any` alias is fine, it's just an identifier), or
an unnamed parameter (Go allows `Find(int)`, but an unnamed param can't be bound via `:name` and would otherwise
produce an invalid generated signature). This exists specifically so a bad signature fails at generation time with a
clear message instead of producing code that doesn't actually implement the source interface, or fails downstream
with a cryptic Go compiler error. See `internal/parser_test.go`'s
`TestParseInterface_Rejects*`/`TestParseInterface_*SliceReturn*` tests.

## Config file format

```yaml
dialect: mysql   # mysql | postgres | sqlite — anything else is a config error
repositories:
  pkg/UserRepository: pkg/repositories_gen.go
  pkg/CarRepository: pkg/repositories_gen.go
```

Key format is `<relative-dir>/<InterfaceName>`. Multiple interfaces can share one output file; when they do, `Runner`
processes repository keys in sorted order so the generated file's struct/method order is deterministic across runs
(`cfg.Repositories` is a map, so without sorting the order would vary run to run).

## Generated code details

- Accepts `*simplesql.ConnectionManager` in the constructor. Every method calls `cm.DB(ctx)` for its `simplesql.Querier`,
  which resolves to whichever `*sql.Tx` (if any) `ctx` carries, else the wrapped `*sql.DB` — so one repository
  instance works both inside and outside a transaction
- If the first parameter is `context.Context` it is forwarded to `QueryContext`/`ExecContext`; otherwise
  `context.Background()` is used
- All DB errors are wrapped with `errors.WithStack` from `github.com/cockroachdb/errors`
- `sql.Rows.Close()`'s error is never silently discarded: `ReturnSingle` methods use a named `_err` return (all
  generated identifiers are `_`-prefixed to avoid colliding with anything meaningful — a named return is no
  exception) with a deferred `_err = errors.Join(_err, _rows.Close())`, and `simplesql.ScanRows` (used by `ReturnSlice`
  methods) does the same internally with its own `err` — a `Close()` failure surfaces (joined onto any earlier
  error) instead of vanishing via a bare `defer rows.Close()`. Note this is `errors.Join` (real multi-error,
  `Unwrap() []error`), not `errors.CombineErrors` — the latter attaches the second error as a "secondary" that's
  invisible to both `Error()` and `errors.Is`/`As`, which silently defeats the whole point of not discarding it.
- Result structs are scanned by `db` struct tag via `simplesql.FieldPointers`, which validates `dest` (non-nil pointer
  to struct) and returns an error rather than panicking on bad input; `simplesql.ScanRows` does the same for its
  `*[]T`/`*[]*T` destination
- A `ReturnSingle` method (`selectOneTmpl`) checks `_rows.Err()` when `_rows.Next()` returns false, so a genuine
  rows-iteration error isn't mistaken for "no row found" and silently turned into `(nil, nil)`
- If a param/return type is referenced through an aliased import (`alias.Type`), `parser.go`'s `usedImports` preserves
  that alias into the generated file's import block (`Import{Path, Alias}` in `types.go`) — since the alias generally
  won't match the imported package's own default name, dropping it would make the generated file fail to compile
- Output is passed through `go/format` so it is always gofmt-clean

`ConnectionManager.StartTransaction` (`simplesql/connection_manager.go`) commits on a `nil` callback return, rolls back
and returns `errors.Join(cbErr, tx.Rollback())` otherwise (so a `Rollback()` failure stays discoverable alongside the
callback error, not hidden behind it), is reentrant (a nested call reuses ctx's existing `*sql.Tx` instead of
starting a second one — only the outermost call owns `tx` and its commit/rollback), and rolls back before
repropagating if the callback panics, so a panicking callback can't leave the transaction open. See
`simplesql/connection_manager_test.go` for the sqlmock-backed tests covering each of these paths, including the
two-errors-at-once case that distinguishes `errors.Join` from `errors.CombineErrors`.
