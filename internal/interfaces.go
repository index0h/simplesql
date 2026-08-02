package internal

import (
	"go/ast"
	"go/token"
)

//go:generate go tool mockgen -source=interfaces.go -destination=mocks_test.go -package=internal

// Reader reads Go source files from a directory.
type Reader interface {
	ReadDir(dir string) (*token.FileSet, []*ast.File, error)
}

// Parser parses Go source files to extract interface definitions.
type Parser interface {
	ParseInterface(dir, ifaceName string) (*Interface, error)
}

// Compiler compiles SQL template strings into Go source fragments.
type Compiler interface {
	CompileBody(m *Method) (body string, needsStrconv bool, err error)
	// Dialect returns the SQL dialect this Compiler was constructed for, so the generator
	// can pick a dialect-specific method template (e.g. Postgres INSERT needs
	// "RETURNING id" + QueryRowContext instead of ExecContext + LastInsertId).
	Dialect() Dialect
}

// Generator generates Go source from parsed interface definitions.
type Generator interface {
	Generate(packageName string, requests []*Request) ([]byte, error)
}
