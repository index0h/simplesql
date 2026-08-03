# simplesql

[![CI](https://github.com/index0h/simplesql/actions/workflows/ci.yml/badge.svg)](https://github.com/index0h/simplesql/actions/workflows/ci.yml)

`simplesql` is a CLI code generator that turns Go repository interfaces with SQL comments into fully working `database/sql` implementations. The CLI entrypoint lives in `cmd/simplesql`.

You write the interface and the queries. `simplesql` writes the boilerplate.

## Installation

```bash
go install github.com/index0h/simplesql/cmd/simplesql@latest
# or
go get -tool github.com/index0h/simplesql/cmd/simplesql
```

## Quick start

**1. Define your interface with SQL comments**

```go
// internal/repo/user_repository.go
package repo

import "context"

type User struct {
    ID      int    `db:"id"`
    Name    string `db:"name"`
    Enabled bool   `db:"enabled"`
}

type UserRepository interface {
    // @sql SELECT * FROM users WHERE id = :id
    FindById(ctx context.Context, id int) (*User, error)

    // @sql SELECT * FROM users
    // WHERE 1=1
    // {{if enabled != nil}}AND enabled = :enabled{{end}}
    FindAll(ctx context.Context, enabled *bool) ([]*User, error)

    // @sql INSERT INTO users (name, enabled) VALUES (:name, :enabled)
    Create(ctx context.Context, name string, enabled bool) (int, error)

    // @sql DELETE FROM users WHERE id = :id
    DeleteById(ctx context.Context, id int) (int, error)
}
```

**2. Create a config file**

```yaml
# simplesql.yaml
dialect: mysql
repositories:
  internal/repo/UserRepository: internal/repo/user_repository_gen.go
```

**3. Run the generator**

```bash
simplesql simplesql.yaml
```

**4. Use the generated code**

```go
package main

import "github.com/index0h/simplesql/simplesql"

func main() {
    // ...
    db, _ := sql.Open("mysql", dsn)
    cm := simplesql.NewConnectionManager(db)
    repo := repo.NewUserRepository(cm)
    
    user, err := repo.FindById(ctx, 42)
    users, err := repo.FindAll(ctx, nil)
    id, err := repo.Create(ctx, "Alice", true)
	//...
}
```

The same `cm` also drives transactions — see [Transactions](#transactions) below.

## SQL template syntax

### The `@sql` tag

Every method's query starts at an `@sql` marker inside its doc comment. Everything before `@sql` is a plain comment and is ignored by the generator; everything from `@sql` onward, to the end of the comment block, is the query template. Methods with no `@sql` tag are skipped — useful for interface methods that aren't backed by a generated query.

```go
// FindById looks up a user by primary key.
// @sql SELECT * FROM users WHERE id = :id
FindById(ctx context.Context, id int) (*User, error)
```

`@sql` doesn't have to start the comment or be on its own line — it can follow prose on the same line, or sit on its own line before the query begins on the next.

### Bound parameters

Named parameters (`:name`) are rewritten to the dialect's placeholder at generation time — no runtime parsing.

```
MySQL / SQLite:  :id  →  ?
Postgres:        :id  →  $1, $2, …
```

### Direct substitution

`{{paramName}}` injects a parameter directly into the query string. The parameter must be `string` or `*string`. Use this for column names, table names, or other structural parts of the query that cannot be bound.

```go
// @sql SELECT {{col}} FROM users
SelectColumn(col string) ([]*User, error)
```

### Conditional blocks

`{{if <expr>}}...{{else if <expr>}}...{{else}}...{{end}}` generates a Go `if` statement in the produced code. Any valid Go boolean expression works.

```go
// @sql SELECT * FROM users WHERE 1=1
// {{if name != ""}}AND name = :name{{end}}
// {{if enabled != nil}}AND enabled = :enabled{{end}}
FindAll(name string, enabled *bool) ([]*User, error)
```

## Return type conventions

| Method signature | Query | Behavior |
|-----------------|-------|----------|
| `(*T, error)` | SELECT | returns `nil, nil` when no row is found |
| `([]*T, error)` or `([]T, error)` | SELECT | returns empty slice when no rows found |
| `(int, error)` | INSERT | returns last insert ID |
| `(int, error)` | UPDATE / DELETE | returns rows affected |
| `(error)` | any | executes and returns error |
| `()` | any | fire and forget |

A slice return can be either a slice of pointers or a slice of values — pick whichever fits; both are scanned the
same way:

```go
// @sql SELECT * FROM users
FindAllPointers(ctx context.Context) ([]*User, error)

// @sql SELECT * FROM users
FindAllValues(ctx context.Context) ([]User, error)
```

Any other signature (a bare, non-pointer single return like `(User, error)` — unlike slices, there's no value
counterpart for the single-row case, since there'd be no sensible zero value for "no row found"; a second return
value that isn't `error`; a param or return type simplesql can't express, like `interface{}`) is rejected at
generation time with a clear error, rather than generating code that silently doesn't match the interface.

On Postgres, `(int, error)` on an `INSERT` works by appending `RETURNING id` to the query and reading it back with
`QueryRowContext` + `Scan` — Postgres has no `LastInsertId()` equivalent. This assumes the table's primary key column
is named `id`.

## Context support

If the first parameter is `context.Context`, it is forwarded to `QueryContext`/`ExecContext`. Otherwise `context.Background()` is used.

```go
// WITH context
FindById(ctx context.Context, id int) (*User, error)

// WITHOUT context — context.Background() is used internally
FindById(id int) (*User, error)
```

## Result scanning

Rows are scanned by matching SQL column names to struct fields using the `db` tag.

```go
type User struct {
    ID      int    `db:"id"`
    Name    string `db:"name"`
    Enabled bool   `db:"enabled"`
}
```

For a join, the result struct can embed each entity directly instead of redeclaring every column flat — embedded
struct fields are flattened recursively when matching columns:

```go
type Balance struct {
    UserID int `db:"user_id"`
    Amount int `db:"amount"`
}

type UserBalance struct {
    User
    Balance
}

// @sql SELECT u.id, u.name, u.enabled, b.user_id, b.amount FROM users u
// INNER JOIN balances b ON b.user_id = u.id
// WHERE u.id = :id
FindWithBalance(ctx context.Context, id int) (*UserBalance, error)
```

## Config reference

```yaml
dialect: mysql          # mysql | postgres | sqlite — anything else is a config error

repositories:
  # <relative-dir>/<InterfaceName>: <output-file>
  internal/repo/UserRepository: internal/repo/user_repository_gen.go
  internal/repo/OrderRepository: internal/repo/order_repository_gen.go

  # Multiple interfaces can share one output file
  internal/repo/TagRepository: internal/repo/misc_gen.go
  internal/repo/AuditRepository: internal/repo/misc_gen.go
```

Paths are relative to the config file's directory.

To customise the generated struct name add a `struct_name` field:

```yaml
repositories:
  internal/repo/UserRepository:
    output: internal/repo/user_repository_gen.go
    struct_name: userRepo
```

## Error handling

All errors returned from the database are wrapped with `errors.WithStack` from `github.com/cockroachdb/errors`, preserving full stack traces.

## Transactions

The generated constructor takes a `*simplesql.ConnectionManager`, not a `*sql.DB`/`*sql.Tx` directly. Every generated
method calls `cm.DB(ctx)` to get its `simplesql.Querier`, which resolves to whichever transaction (if any) `ctx`
carries — so the same repository instance transparently works both inside and outside a transaction, with no need to
construct a second instance around a `*sql.Tx`.

Transactions are started through `cm.StartTransaction`, which commits if the callback returns `nil` and rolls back
(returning the combined error) otherwise:

```go
cm := simplesql.NewConnectionManager(db)
repo := repo.NewUserRepository(cm)

err := cm.StartTransaction(ctx, func(ctx context.Context) error {
    id, err := repo.Create(ctx, "Alice", true)
    if err != nil {
        return err
    }
    _, err = repo.DeleteById(ctx, id)
    return err // nil commits, non-nil rolls back
})
```

`StartTransaction` is reentrant: if `ctx` already carries a transaction (e.g. an outer `StartTransaction` call), a
nested call runs its callback directly against that same transaction instead of opening a second one, so helper
functions that themselves wrap calls in `StartTransaction` compose safely whether or not they're already inside one.

If the callback panics, `StartTransaction` rolls back the transaction before repropagating the panic — a panicking
callback never leaves the transaction open.

## Testing

```bash
go test ./...              # unit tests
go test -race ./...        # unit tests with the race detector
```

`examples/{mysql,postgres,sqlite}/` is a fuller worked reference: the same repository interface generated for all
three dialects, exercising every return kind, bound params, direct substitution, conditionals, transactions, and
joins scanned into an embedded-struct result. Its tests run against real databases and are gated behind a
`functional` build tag, so they're excluded from the commands above:

```bash
docker compose up -d --wait      # starts MySQL + Postgres (SQLite needs no container)
go test -tags functional ./examples/...
docker compose down
```
