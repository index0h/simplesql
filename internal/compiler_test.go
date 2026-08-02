package internal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// compile is a test helper: builds a Method, compiles it, and returns the body.
func compile(t *testing.T, sql string, params []Param, retKind ReturnKind, dialect Dialect) string {
	t.Helper()
	m := &Method{
		Name:       "Test",
		SQL:        sql,
		QueryKind:  QuerySelect,
		Params:     params,
		ReturnKind: retKind,
		ReturnType: "Row",
	}
	body, _, err := NewCompiler(dialect).CompileBody(m)
	require.NoError(t, err)
	return body
}

func assertContains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		require.Contains(t, body, w)
	}
}

func assertNotContains(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		require.NotContains(t, body, u)
	}
}

// ── MySQL ─────────────────────────────────────────────────────────────────────

func TestMySQL_SimpleBoundParam(t *testing.T) {
	body := compile(t,
		"SELECT * FROM `users` WHERE `id` = :id",
		[]Param{{Name: "id", Type: "int"}},
		ReturnSingle,
		DialectMySQL,
	)
	assertContains(t, body,
		`_sb.WriteString("SELECT * FROM `+"`"+`users`+"`"+` WHERE `+"`"+`id`+"`"+` = ?")`,
		`_args = append(_args, id)`,
	)
	assertNotContains(t, body, `$1`, `_argN`)
}

func TestMySQL_MultipleParams(t *testing.T) {
	body := compile(t,
		"SELECT * FROM `users` WHERE `id` = :id AND `name` = :name",
		[]Param{
			{Name: "id", Type: "int"},
			{Name: "name", Type: "string"},
		},
		ReturnSlice,
		DialectMySQL,
	)
	assertContains(t, body,
		`_args = append(_args, id)`,
		`_args = append(_args, name)`,
	)
	require.GreaterOrEqual(t, strings.Count(body, `?`), 2, "expected at least 2 ? placeholders")
}

func TestMySQL_ConditionalClause(t *testing.T) {
	body := compile(t,
		"SELECT * FROM `users` WHERE 1=1\n{{if enabled != nil}}AND `enabled` = :enabled{{end}}",
		[]Param{{Name: "enabled", Type: "*bool", IsPtr: true}},
		ReturnSlice,
		DialectMySQL,
	)
	assertContains(t, body,
		`if enabled != nil {`,
		`_sb.WriteString("AND `+"`"+`enabled`+"`"+` = ?")`,
		`_args = append(_args, enabled)`,
		`}`,
	)
}

func TestMySQL_ConditionalElse(t *testing.T) {
	body := compile(t,
		"SELECT * FROM `users` WHERE {{if active}}active = 1{{else}}active = 0{{end}}",
		[]Param{{Name: "active", Type: "bool"}},
		ReturnSlice,
		DialectMySQL,
	)
	assertContains(t, body,
		`if active {`,
		`_sb.WriteString("active = 1")`,
		`} else {`,
		`_sb.WriteString("active = 0")`,
	)
}

func TestMySQL_ConditionalElseIf(t *testing.T) {
	body := compile(t,
		"WHERE 1=1\n{{if status == \"active\"}}AND status = 1{{else if status == \"pending\"}}AND status = 2{{else}}AND status = 0{{end}}",
		[]Param{{Name: "status", Type: "string"}},
		ReturnSlice,
		DialectMySQL,
	)
	assertContains(t, body,
		`if status == "active" {`,
		`} else if status == "pending" {`,
		`} else {`,
	)
}

func TestMySQL_DirectSubstitution(t *testing.T) {
	body := compile(t,
		"SELECT {{col}} FROM `users`",
		[]Param{{Name: "col", Type: "string"}},
		ReturnSlice,
		DialectMySQL,
	)
	assertContains(t, body, `_sb.WriteString(col)`)
}

// TestDirectSubstitution_RejectsNonStringType guards against a regression where a
// direct-substitution param of any type other than string/*string silently fell back to
// strconv.FormatInt(int64(x), 10) — generating uncompilable code for anything non-numeric
// (bool, time.Time, structs, ...) despite the documented contract being string-only.
func TestDirectSubstitution_RejectsNonStringType(t *testing.T) {
	m := &Method{
		Name:       "Test",
		SQL:        "SELECT {{flag}} FROM users",
		QueryKind:  QuerySelect,
		Params:     []Param{{Name: "flag", Type: "bool"}},
		ReturnKind: ReturnSlice,
		ReturnType: "Row",
	}
	_, _, err := NewCompiler(DialectMySQL).CompileBody(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be string or *string")
}

func TestMySQL_Insert(t *testing.T) {
	body := compile(t,
		"INSERT INTO `users` (`name`, `enabled`) VALUES (:name, :enabled)",
		[]Param{
			{Name: "name", Type: "string"},
			{Name: "enabled", Type: "bool"},
		},
		ReturnRowsOrID,
		DialectMySQL,
	)
	assertContains(t, body,
		`_args = append(_args, name)`,
		`_args = append(_args, enabled)`,
	)
}

func TestMySQL_UpdateDelete(t *testing.T) {
	body := compile(t,
		"UPDATE `users` SET `name` = :name WHERE `id` = :id",
		[]Param{
			{Name: "name", Type: "string"},
			{Name: "id", Type: "int"},
		},
		ReturnRowsOrID,
		DialectMySQL,
	)
	assertContains(t, body,
		`_args = append(_args, name)`,
		`_args = append(_args, id)`,
	)
}

func TestMySQL_NoParams(t *testing.T) {
	body := compile(t,
		"SELECT * FROM `users`",
		nil,
		ReturnSlice,
		DialectMySQL,
	)
	assertContains(t, body, `_sb.WriteString("SELECT * FROM`)
	assertNotContains(t, body, `_args = append`)
}

// TestMySQL_TypeCastNotTreatedAsParam guards against a regression where a Postgres-style
// "::" type cast (also just inert punctuation for MySQL/SQLite) was mis-parsed as a bound
// param named after the cast target, e.g. "amount::numeric" was read as ":numeric".
func TestMySQL_TypeCastNotTreatedAsParam(t *testing.T) {
	body := compile(t,
		"SELECT amount::numeric FROM t WHERE id = :id",
		[]Param{{Name: "id", Type: "int"}},
		ReturnSlice,
		DialectMySQL,
	)
	assertContains(t, body,
		`_sb.WriteString("SELECT amount::numeric FROM t WHERE id = ?")`,
		`_args = append(_args, id)`,
	)
	assertNotContains(t, body, `_args = append(_args, numeric)`)
}

// ── PostgreSQL ────────────────────────────────────────────────────────────────

func TestPostgres_SingleParam(t *testing.T) {
	body := compile(t,
		"SELECT * FROM users WHERE id = :id",
		[]Param{{Name: "id", Type: "int"}},
		ReturnSingle,
		DialectPostgres,
	)
	assertContains(t, body,
		`_sb.WriteString("$" + strconv.Itoa(len(_args)+1))`,
		`_args = append(_args, id)`,
	)
	assertNotContains(t, body, `"?"`, `_argN`)
}

func TestPostgres_NoParams(t *testing.T) {
	body := compile(t,
		"SELECT * FROM users",
		nil,
		ReturnSlice,
		DialectPostgres,
	)
	assertNotContains(t, body, `_argN`, `$1`)
}

// TestPostgres_TypeCastNotTreatedAsParam guards against a regression where a Postgres
// "::" type cast was mis-parsed as a bound param named after the cast target, e.g.
// "amount::numeric" was read as ":numeric" and emitted as an undefined "numeric" identifier.
func TestPostgres_TypeCastNotTreatedAsParam(t *testing.T) {
	body := compile(t,
		"SELECT amount::numeric FROM t WHERE id = :id",
		[]Param{{Name: "id", Type: "int"}},
		ReturnSlice,
		DialectPostgres,
	)
	assertContains(t, body,
		`_sb.WriteString("SELECT amount")`,
		`_sb.WriteString("::")`,
		`_args = append(_args, id)`,
	)
	assertNotContains(t, body, `_args = append(_args, numeric)`)
}

func TestPostgres_MultipleParams(t *testing.T) {
	body := compile(t,
		"SELECT * FROM users WHERE id = :id AND status = :status",
		[]Param{
			{Name: "id", Type: "int"},
			{Name: "status", Type: "string"},
		},
		ReturnSlice,
		DialectPostgres,
	)
	require.Equal(t, 2, strings.Count(body, `strconv.Itoa(len(_args)+1)`), "expected 2 positional placeholders")
	assertContains(t, body,
		`_args = append(_args, id)`,
		`_args = append(_args, status)`,
	)
}

func TestPostgres_RepeatedParam(t *testing.T) {
	body := compile(t,
		"WHERE a = :x OR b = :x",
		[]Param{{Name: "x", Type: "int"}},
		ReturnSlice,
		DialectPostgres,
	)
	require.Equal(t, 2, strings.Count(body, `strconv.Itoa(len(_args)+1)`), "expected 2 positional placeholders for repeated param")
	require.Equal(t, 2, strings.Count(body, `_args = append(_args, x)`), "expected 2 _args appends for repeated param")
}

func TestPostgres_ConditionalClause(t *testing.T) {
	body := compile(t,
		"SELECT * FROM users WHERE 1=1\n{{if name != \"\"}}AND name = :name{{end}}",
		[]Param{{Name: "name", Type: "string"}},
		ReturnSlice,
		DialectPostgres,
	)
	assertContains(t, body,
		`if name != "" {`,
		`strconv.Itoa(len(_args)+1)`,
		`_args = append(_args, name)`,
	)
}

func TestPostgres_Insert(t *testing.T) {
	body := compile(t,
		"INSERT INTO users (name, age) VALUES (:name, :age)",
		[]Param{
			{Name: "name", Type: "string"},
			{Name: "age", Type: "int"},
		},
		ReturnRowsOrID,
		DialectPostgres,
	)
	require.Equal(t, 2, strings.Count(body, `strconv.Itoa(len(_args)+1)`), "expected 2 positional placeholders for insert")
}

// TestPostgres_InsertAppendsReturningID guards the fix for Postgres's missing
// LastInsertId() support: an INSERT method with a (int, error) return must get
// "RETURNING id" appended so the generator can read the new row's id back via
// QueryRowContext+Scan instead of ExecContext+LastInsertId (see templates.go
// insertIDReturningTmpl).
func TestPostgres_InsertAppendsReturningID(t *testing.T) {
	m := &Method{
		Name:       "Create",
		SQL:        "INSERT INTO users (name) VALUES (:name)",
		QueryKind:  QueryInsert,
		Params:     []Param{{Name: "name", Type: "string"}},
		ReturnKind: ReturnRowsOrID,
		ReturnType: "int",
	}
	body, _, err := NewCompiler(DialectPostgres).CompileBody(m)
	require.NoError(t, err)
	assertContains(t, body, `_sb.WriteString(" RETURNING id")`)
}

// TestPostgres_InsertDoesNotDoubleReturning ensures a hand-written RETURNING clause
// isn't duplicated.
func TestPostgres_InsertDoesNotDoubleReturning(t *testing.T) {
	m := &Method{
		Name:       "Create",
		SQL:        "INSERT INTO users (name) VALUES (:name) RETURNING id, created_at",
		QueryKind:  QueryInsert,
		Params:     []Param{{Name: "name", Type: "string"}},
		ReturnKind: ReturnRowsOrID,
		ReturnType: "int",
	}
	body, _, err := NewCompiler(DialectPostgres).CompileBody(m)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(body, "RETURNING"), "must not append a second RETURNING clause")
}

// TestMySQL_InsertDoesNotAppendReturning ensures the Postgres-only RETURNING behavior
// doesn't leak into other dialects, which don't support it (MySQL) or don't need it
// (SQLite already gets LastInsertId from the driver).
func TestMySQL_InsertDoesNotAppendReturning(t *testing.T) {
	m := &Method{
		Name:       "Create",
		SQL:        "INSERT INTO users (name) VALUES (:name)",
		QueryKind:  QueryInsert,
		Params:     []Param{{Name: "name", Type: "string"}},
		ReturnKind: ReturnRowsOrID,
		ReturnType: "int",
	}
	body, _, err := NewCompiler(DialectMySQL).CompileBody(m)
	require.NoError(t, err)
	assertNotContains(t, body, "RETURNING")
}

// ── SQLite ────────────────────────────────────────────────────────────────────

func TestSQLite_SimpleBoundParam(t *testing.T) {
	body := compile(t,
		"SELECT * FROM users WHERE id = :id",
		[]Param{{Name: "id", Type: "int"}},
		ReturnSingle,
		DialectSQLite,
	)
	assertContains(t, body,
		`"SELECT * FROM users WHERE id = ?"`,
		`_args = append(_args, id)`,
	)
	assertNotContains(t, body, `$1`, `_argN`)
}

func TestSQLite_Insert(t *testing.T) {
	body := compile(t,
		"INSERT INTO users (name) VALUES (:name)",
		[]Param{{Name: "name", Type: "string"}},
		ReturnRowsOrID,
		DialectSQLite,
	)
	assertContains(t, body,
		`"INSERT INTO users (name) VALUES (?)"`,
		`_args = append(_args, name)`,
	)
}

func TestSQLite_Conditional(t *testing.T) {
	body := compile(t,
		"SELECT * FROM notes WHERE 1=1\n{{if archived != nil}}AND archived = :archived{{end}}",
		[]Param{{Name: "archived", Type: "*bool", IsPtr: true}},
		ReturnSlice,
		DialectSQLite,
	)
	assertContains(t, body,
		`if archived != nil {`,
		`"AND archived = ?"`,
		`_args = append(_args, archived)`,
	)
}

// ── Error cases ───────────────────────────────────────────────────────────────

func TestError_UnclosedAction(t *testing.T) {
	m := &Method{
		Name:   "Test",
		SQL:    "SELECT * FROM users {{if id > 0",
		Params: []Param{{Name: "id", Type: "int"}},
	}
	_, _, err := NewCompiler(DialectMySQL).CompileBody(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unclosed")
}

func TestError_MissingEnd(t *testing.T) {
	m := &Method{
		Name:   "Test",
		SQL:    "SELECT * FROM users {{if id > 0}}AND id = :id",
		Params: []Param{{Name: "id", Type: "int"}},
	}
	_, _, err := NewCompiler(DialectMySQL).CompileBody(m)
	require.Error(t, err)
}

// ── Full generation ───────────────────────────────────────────────────────────

func TestGenerate_MySQL_FullInterface(t *testing.T) {
	iface := &Interface{
		Name:        "UserRepository",
		PackageName: "repo",
		Methods: []Method{
			{
				Name:       "FindById",
				SQL:        "SELECT * FROM `users` WHERE `id` = :id",
				QueryKind:  QuerySelect,
				Params:     []Param{{Name: "id", Type: "int"}},
				ReturnKind: ReturnSingle,
				ReturnType: "User",
			},
			{
				Name:       "FindAll",
				SQL:        "SELECT * FROM `users` WHERE 1=1\n{{if enabled != nil}}AND `enabled` = :enabled{{end}}",
				QueryKind:  QuerySelect,
				Params:     []Param{{Name: "enabled", Type: "*bool", IsPtr: true}},
				ReturnKind: ReturnSlice,
				ReturnType: "User",
			},
			{
				Name:       "Create",
				SQL:        "INSERT INTO `users` (`name`) VALUES (:name)",
				QueryKind:  QueryInsert,
				Params:     []Param{{Name: "name", Type: "string"}},
				ReturnKind: ReturnRowsOrID,
				ReturnType: "int",
			},
			{
				Name:       "DeleteById",
				SQL:        "DELETE FROM `users` WHERE `id` = :id",
				QueryKind:  QueryDelete,
				Params:     []Param{{Name: "id", Type: "int"}},
				ReturnKind: ReturnRowsOrID,
				ReturnType: "int",
			},
			{
				Name:       "Update",
				SQL:        "UPDATE `users` SET `name` = :name WHERE `id` = :id",
				QueryKind:  QueryUpdate,
				Params:     []Param{{Name: "name", Type: "string"}, {Name: "id", Type: "int"}},
				ReturnKind: ReturnError,
			},
		},
	}

	src, err := NewGenerator(zap.NewNop(), NewCompiler(DialectMySQL)).Generate("repo", []*Request{{Interface: iface}})
	require.NoError(t, err, "source:\n%s", src)

	assertContainsStr(t, string(src),
		"package repo",
		"cm *querier.ConnectionManager",
		"func NewUserRepository(cm *querier.ConnectionManager)",
		"func (s *userRepositoryImpl) FindById(",
		"func (s *userRepositoryImpl) FindAll(",
		"func (s *userRepositoryImpl) Create(",
		"func (s *userRepositoryImpl) DeleteById(",
		"func (s *userRepositoryImpl) Update(",
		"s.cm.DB(",
		"if enabled != nil {",
		"LastInsertId()",
		"RowsAffected()",
		// FindById (ReturnSingle) must join _rows.Close()'s error instead of discarding it.
		"(_ *User, _err error) {",
		"_err = errors.Join(_err, _rows.Close())",
		// ...and must not treat a rows-iteration error as "not found".
		"if _err := _rows.Err(); _err != nil {",
	)
}

func TestGenerate_Postgres_FullInterface(t *testing.T) {
	iface := &Interface{
		Name:        "OrderRepository",
		PackageName: "repo",
		Methods: []Method{
			{
				Name:       "FindByUserAndStatus",
				SQL:        "SELECT * FROM orders WHERE user_id = :userID AND status = :status",
				QueryKind:  QuerySelect,
				Params:     []Param{{Name: "userID", Type: "int"}, {Name: "status", Type: "string"}},
				ReturnKind: ReturnSlice,
				ReturnType: "Order",
			},
			{
				Name:       "Create",
				SQL:        "INSERT INTO orders (user_id, total) VALUES (:userID, :total)",
				QueryKind:  QueryInsert,
				Params:     []Param{{Name: "userID", Type: "int"}, {Name: "total", Type: "int"}},
				ReturnKind: ReturnRowsOrID,
				ReturnType: "int",
			},
		},
	}

	src, err := NewGenerator(zap.NewNop(), NewCompiler(DialectPostgres)).Generate("repo", []*Request{{Interface: iface}})
	require.NoError(t, err, "source:\n%s", src)

	assertContainsStr(t, string(src),
		"strconv",
		`"$" + strconv.Itoa(len(_args)+1)`,
		`_sb.WriteString(" RETURNING id")`,
		"QueryRowContext(",
		".Scan(&_id)",
	)
	assertNotContainsStr(t, string(src), `"?"`, `_argN`, "LastInsertId")
}

func TestGenerate_SQLite_FullInterface(t *testing.T) {
	iface := &Interface{
		Name:        "NoteRepository",
		PackageName: "repo",
		Methods: []Method{
			{
				Name:       "FindAll",
				SQL:        "SELECT * FROM notes WHERE 1=1\n{{if tag != \"\"}}AND tag = :tag{{end}}",
				QueryKind:  QuerySelect,
				Params:     []Param{{Name: "tag", Type: "string"}},
				ReturnKind: ReturnSlice,
				ReturnType: "Note",
			},
			{
				Name:       "Save",
				SQL:        "INSERT INTO notes (body) VALUES (:body)",
				QueryKind:  QueryInsert,
				Params:     []Param{{Name: "body", Type: "string"}},
				ReturnKind: ReturnRowsOrID,
				ReturnType: "int",
			},
		},
	}

	src, err := NewGenerator(zap.NewNop(), NewCompiler(DialectSQLite)).Generate("repo", []*Request{{Interface: iface}})
	require.NoError(t, err, "source:\n%s", src)

	assertContainsStr(t, string(src),
		`if tag != "" {`,
		`"AND tag = ?"`,
		`"INSERT INTO notes (body) VALUES (?)"`,
	)
	assertNotContainsStr(t, string(src), `_argN`, `strconv.Itoa`)
}

func TestGenerate_WithContext(t *testing.T) {
	iface := &Interface{
		Name:        "ItemRepository",
		PackageName: "repo",
		Methods: []Method{
			{
				Name:      "FindById",
				SQL:       "SELECT * FROM items WHERE id = :id",
				QueryKind: QuerySelect,
				Params: []Param{
					{Name: "ctx", Type: "context.Context"},
					{Name: "id", Type: "int"},
				},
				ReturnKind: ReturnSingle,
				ReturnType: "Item",
			},
		},
	}

	src, err := NewGenerator(zap.NewNop(), NewCompiler(DialectMySQL)).Generate("repo", []*Request{{Interface: iface}})
	require.NoError(t, err, "source:\n%s", src)

	assertContainsStr(t, string(src), "QueryContext(ctx,")
	assertNotContainsStr(t, string(src), "context.Background()")
}

func TestGenerate_WithoutContext(t *testing.T) {
	iface := &Interface{
		Name:        "ItemRepository",
		PackageName: "repo",
		Methods: []Method{
			{
				Name:       "FindById",
				SQL:        "SELECT * FROM items WHERE id = :id",
				QueryKind:  QuerySelect,
				Params:     []Param{{Name: "id", Type: "int"}},
				ReturnKind: ReturnSingle,
				ReturnType: "Item",
			},
		},
	}

	src, err := NewGenerator(zap.NewNop(), NewCompiler(DialectMySQL)).Generate("repo", []*Request{{Interface: iface}})
	require.NoError(t, err, "source:\n%s", src)

	assertContainsStr(t, string(src), "QueryContext(context.Background(),")
}

func TestGenerate_AllReturnKinds(t *testing.T) {
	iface := &Interface{
		Name:        "MixedRepository",
		PackageName: "repo",
		Methods: []Method{
			{Name: "GetOne", SQL: "SELECT * FROM t WHERE id = :id", QueryKind: QuerySelect,
				Params: []Param{{Name: "id", Type: "int"}}, ReturnKind: ReturnSingle, ReturnType: "T"},
			{Name: "GetMany", SQL: "SELECT * FROM t", QueryKind: QuerySelect,
				ReturnKind: ReturnSlice, ReturnType: "T"},
			{Name: "Insert", SQL: "INSERT INTO t (x) VALUES (:x)", QueryKind: QueryInsert,
				Params: []Param{{Name: "x", Type: "int"}}, ReturnKind: ReturnRowsOrID},
			{Name: "Update", SQL: "UPDATE t SET x = :x", QueryKind: QueryUpdate,
				Params: []Param{{Name: "x", Type: "int"}}, ReturnKind: ReturnRowsOrID},
			{Name: "Delete", SQL: "DELETE FROM t WHERE id = :id", QueryKind: QueryDelete,
				Params: []Param{{Name: "id", Type: "int"}}, ReturnKind: ReturnError},
			{Name: "Purge", SQL: "DELETE FROM t", QueryKind: QueryDelete,
				ReturnKind: ReturnNothing},
		},
	}

	src, err := NewGenerator(zap.NewNop(), NewCompiler(DialectMySQL)).Generate("repo", []*Request{{Interface: iface}})
	require.NoError(t, err, "source:\n%s", src)

	assertContainsStr(t, string(src),
		"func (s *mixedRepositoryImpl) GetOne(",
		"func (s *mixedRepositoryImpl) GetMany(",
		"LastInsertId()",
		"RowsAffected()",
		"func (s *mixedRepositoryImpl) Delete(",
		"func (s *mixedRepositoryImpl) Purge(",
	)
}

func assertContainsStr(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		require.Contains(t, body, w)
	}
}

func assertNotContainsStr(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		require.NotContains(t, body, u)
	}
}
