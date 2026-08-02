package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const testSrc = `package testpkg

type User struct {
	ID   int    ` + "`db:\"id\"`" + `
	Name string ` + "`db:\"name\"`" + `
}

type UserRepository interface {
	// @sql SELECT * FROM ` + "`users`" + ` WHERE ` + "`id`" + ` = :id
	FindById(id int) (*User, error)

	// @sql SELECT * FROM ` + "`users`" + `
	FindAll() ([]*User, error)

	// @sql INSERT INTO ` + "`users`" + ` (` + "`name`" + `) VALUES (:name)
	Create(name string) (int, error)

	// @sql DELETE FROM ` + "`users`" + ` WHERE ` + "`id`" + ` = :id
	Delete(id int) (int, error)
}
`

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
}

func newTestParser() Parser {
	log := zap.NewNop()
	return NewParser(log, NewReader(log))
}

func TestParseInterface(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", testSrc)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "UserRepository")
	require.NoError(t, err)
	require.Equal(t, "UserRepository", iface.Name)
	require.Len(t, iface.Methods, 4)

	byName := map[string]Method{}
	for _, m := range iface.Methods {
		byName[m.Name] = m
	}

	cases := []struct {
		method     string
		kind       QueryKind
		returnKind ReturnKind
		returnType string
	}{
		{"FindById", QuerySelect, ReturnSingle, "User"},
		{"FindAll", QuerySelect, ReturnSlice, "User"},
		{"Create", QueryInsert, ReturnRowsOrID, "int"},
		{"Delete", QueryDelete, ReturnRowsOrID, "int"},
	}

	for _, c := range cases {
		m, ok := byName[c.method]
		require.Truef(t, ok, "method %q not found", c.method)
		require.Equalf(t, c.kind, m.QueryKind, "%s: QueryKind", c.method)
		require.Equalf(t, c.returnKind, m.ReturnKind, "%s: ReturnKind", c.method)
		require.Equalf(t, c.returnType, m.ReturnType, "%s: ReturnType", c.method)
	}
}

func TestParseInterface_Params(t *testing.T) {
	src := `package testpkg
type Repo interface {
	// @sql SELECT * FROM t WHERE id = :id AND name = :name
	Find(id int, name string) ([]*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.Len(t, iface.Methods, 1)

	params := iface.Methods[0].Params
	require.Len(t, params, 2)
	require.Equal(t, "id", params[0].Name)
	require.Equal(t, "int", params[0].Type)
	require.False(t, params[0].IsPtr)
	require.Equal(t, "name", params[1].Name)
	require.Equal(t, "string", params[1].Type)
}

func TestParseInterface_PtrParam(t *testing.T) {
	src := `package testpkg
type Repo interface {
	// @sql SELECT * FROM t WHERE 1=1
	Find(enabled *bool) ([]*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.Len(t, iface.Methods[0].Params, 1)
	param := iface.Methods[0].Params[0]
	require.Equal(t, "*bool", param.Type)
	require.True(t, param.IsPtr)
}

func TestParseInterface_ContextParam(t *testing.T) {
	src := `package testpkg
import "context"
type Repo interface {
	// @sql SELECT * FROM t WHERE id = :id
	Find(ctx context.Context, id int) (*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.Len(t, iface.Methods[0].Params, 2)
	require.Equal(t, "context.Context", iface.Methods[0].Params[0].Type)
}

func TestParseInterface_ImportsOnlyWhatIsUsed(t *testing.T) {
	src := `package testpkg

import (
	"context"
	"fmt"
	_ "some/blank/import"
)

// Debug is unrelated to the repository interface; its use of fmt must not leak
// into Interface.Imports, or the generated file would fail with "fmt imported and not used".
func Debug(v any) string {
	return fmt.Sprintf("%+v", v)
}

type T struct{}

type Repo interface {
	// @sql SELECT * FROM t WHERE id = :id
	Find(ctx context.Context, id int) (*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"context", "some/blank/import"}, iface.Imports)
}

func TestParseInterface_SkipsNoSQL(t *testing.T) {
	src := `package testpkg
type Repo interface {
	// This method has no SQL
	Helper() error
	// @sql SELECT * FROM t
	Find() ([]*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.Len(t, iface.Methods, 1)
	require.Equal(t, "Find", iface.Methods[0].Name)
}

func TestParseInterface_AllReturnKinds(t *testing.T) {
	src := `package testpkg
type Repo interface {
	// @sql SELECT * FROM t WHERE id = :id
	GetOne(id int) (*T, error)
	// @sql SELECT * FROM t
	GetMany() ([]*T, error)
	// @sql INSERT INTO t (x) VALUES (:x)
	Insert(x int) (int, error)
	// @sql DELETE FROM t WHERE id = :id
	Delete(id int) error
	// @sql DELETE FROM t
	Purge()
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.Len(t, iface.Methods, 5)

	byName := map[string]Method{}
	for _, m := range iface.Methods {
		byName[m.Name] = m
	}

	require.Equal(t, ReturnSingle, byName["GetOne"].ReturnKind)
	require.Equal(t, "T", byName["GetOne"].ReturnType)
	require.Equal(t, ReturnSlice, byName["GetMany"].ReturnKind)
	require.Equal(t, "T", byName["GetMany"].ReturnType)
	require.Equal(t, ReturnRowsOrID, byName["Insert"].ReturnKind)
	require.Equal(t, ReturnError, byName["Delete"].ReturnKind)
	require.Equal(t, ReturnNothing, byName["Purge"].ReturnKind)
}

func TestParseInterface_MultilineSQL(t *testing.T) {
	src := `package testpkg
type Repo interface {
	// Finds a record by id.
	// @sql SELECT *
	// FROM t
	// WHERE id = :id
	Find(id int) (*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.NotContains(t, iface.Methods[0].SQL, "Finds a record by id.")
	require.Contains(t, iface.Methods[0].SQL, "SELECT *")
	require.Contains(t, iface.Methods[0].SQL, "FROM t")
	require.Contains(t, iface.Methods[0].SQL, "WHERE id = :id")
}

func TestParseInterface_SqlTagMidLine(t *testing.T) {
	src := `package testpkg
type Repo interface {
	// Finds a record by id. @sql SELECT * FROM t WHERE id = :id
	Find(id int) (*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.Equal(t, "SELECT * FROM t WHERE id = :id", iface.Methods[0].SQL)
	require.Equal(t, QuerySelect, iface.Methods[0].QueryKind)
}

func TestParseInterface_NoSqlTagSkipped(t *testing.T) {
	src := `package testpkg
type Repo interface {
	// SELECT * FROM t WHERE id = :id
	Find(id int) (*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.Empty(t, iface.Methods)
}

func TestParseInterface_NotFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", "package p\n")

	p := newTestParser()
	_, err := p.ParseInterface(dir, "Missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Missing")
}

func TestParseInterface_InvalidDir(t *testing.T) {
	p := newTestParser()
	_, err := p.ParseInterface("/nonexistent/dir", "Repo")
	require.Error(t, err)
}

// ── Return signature validation ────────────────────────────────────────────────

func TestParseInterface_RejectsNonPointerSingleReturn(t *testing.T) {
	src := `package testpkg
type T struct{}
type Repo interface {
	// @sql SELECT * FROM t WHERE id = :id
	Find(id int) (T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	_, err := p.ParseInterface(dir, "Repo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported return type")
}

func TestParseInterface_RejectsNonPointerSliceReturn(t *testing.T) {
	src := `package testpkg
type T struct{}
type Repo interface {
	// @sql SELECT * FROM t
	Find() ([]T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	_, err := p.ParseInterface(dir, "Repo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported return type")
}

func TestParseInterface_RejectsSecondReturnNotError(t *testing.T) {
	src := `package testpkg
type Repo interface {
	// @sql SELECT count(*) FROM t
	Count() (int, int)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	_, err := p.ParseInterface(dir, "Repo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "second return value must be error")
}

func TestParseInterface_RejectsSingleNonErrorReturn(t *testing.T) {
	src := `package testpkg
type Repo interface {
	// @sql SELECT count(*) FROM t
	Count() int
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	_, err := p.ParseInterface(dir, "Repo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported return type")
}

func TestParseInterface_RejectsTooManyReturnValues(t *testing.T) {
	src := `package testpkg
type T struct{}
type Repo interface {
	// @sql SELECT * FROM t
	Find() (*T, int, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	_, err := p.ParseInterface(dir, "Repo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported return signature")
}

func TestParseInterface_RejectsUnsupportedParamType(t *testing.T) {
	src := `package testpkg
type T struct{}
type Repo interface {
	// @sql SELECT * FROM t WHERE data = :data
	Find(data interface{}) (*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	_, err := p.ParseInterface(dir, "Repo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported parameter type")
	require.Contains(t, err.Error(), "data")
}

func TestParseInterface_RejectsUnsupportedVariadicParamType(t *testing.T) {
	src := `package testpkg
type T struct{}
type Repo interface {
	// @sql SELECT * FROM t
	Find(ids ...int) (*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	_, err := p.ParseInterface(dir, "Repo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported parameter type")
}

func TestParseInterface_RejectsUnsupportedReturnType(t *testing.T) {
	src := `package testpkg
type Repo interface {
	// @sql SELECT * FROM t
	Find() (interface{}, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	_, err := p.ParseInterface(dir, "Repo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported return type at position 1")
}

// TestParseInterface_AnyParamTypeAccepted confirms the "any" alias (unlike literal
// interface{}) parses fine, since it's just an *ast.Ident to the parser.
func TestParseInterface_AnyParamTypeAccepted(t *testing.T) {
	src := `package testpkg
type T struct{}
type Repo interface {
	// @sql SELECT * FROM t WHERE data = :data
	Find(data any) (*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.Equal(t, "any", iface.Methods[0].Params[0].Type)
}

// TestParseInterface_NoSqlMethodNotGenerated confirms end-to-end (parse + generate) that a
// method without an @sql tag is dropped before it ever reaches the generator, so it produces
// no function in the generated source.
func TestParseInterface_NoSqlMethodNotGenerated(t *testing.T) {
	src := `package pkg
type T struct {
	ID int ` + "`db:\"id\"`" + `
}
type Repo interface {
	// Helper is a plain helper method with no SQL query, must not appear in generated code.
	Helper() error

	// @sql SELECT * FROM t WHERE id = :id
	FindById(id int) (*T, error)
}
`
	dir := t.TempDir()
	writeFile(t, dir, "repo.go", src)

	p := newTestParser()
	iface, err := p.ParseInterface(dir, "Repo")
	require.NoError(t, err)
	require.Len(t, iface.Methods, 1)
	require.Equal(t, "FindById", iface.Methods[0].Name)

	src2, err := NewGenerator(zap.NewNop(), NewCompiler(DialectMySQL)).Generate("pkg", []*Request{{Interface: iface}})
	require.NoError(t, err)
	require.Contains(t, string(src2), "func (s *repoImpl) FindById(")
	require.NotContains(t, string(src2), "Helper")
}
