package internal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/cockroachdb/errors"
	"go.uber.org/zap"
)

type readerImpl struct {
	log *zap.Logger
}

// NewReader returns a new Reader.
func NewReader(log *zap.Logger) Reader {
	return &readerImpl{log: log}
}

// ReadDir reads all .go files in dir and returns their parsed ASTs.
func (r *readerImpl) ReadDir(dir string) (*token.FileSet, []*ast.File, error) {
	r.log.Debug("reading directory", zap.String("dir", dir))

	fset := token.NewFileSet()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, errors.Wrap(err, "read directory")
	}

	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		r.log.Debug("parsing file", zap.String("file", entry.Name()))
		file, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, parser.ParseComments)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "parse %s", entry.Name())
		}
		files = append(files, file)
	}

	return fset, files, nil
}
