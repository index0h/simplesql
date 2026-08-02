package internal

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/cockroachdb/errors"
	"go.uber.org/zap"
)

var sqlKeywords = []string{"SELECT", "INSERT", "UPDATE", "DELETE"}

const sqlTag = "@sql"

type parserImpl struct {
	reader Reader
	log    *zap.Logger
}

// NewParser returns a new Parser.
func NewParser(log *zap.Logger, reader Reader) Parser {
	return &parserImpl{reader: reader, log: log}
}

// ParseInterface parses all .go files in dir and returns the named interface.
func (p *parserImpl) ParseInterface(dir, ifaceName string) (*Interface, error) {
	p.log.Debug("parsing directory", zap.String("dir", dir), zap.String("interface", ifaceName))

	fset, files, err := p.reader.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || typeSpec.Name.Name != ifaceName {
					continue
				}
				iface, ok := typeSpec.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				result, err := p.buildInterface(file.Name.Name, ifaceName, iface)
				if err != nil {
					return nil, err
				}
				result.Imports = p.usedImports(file, result)
				p.log.Info("parsed interface",
					zap.String("interface", ifaceName),
					zap.String("file", fset.Position(file.Pos()).Filename),
					zap.Int("methods", len(result.Methods)),
				)
				return result, nil
			}
		}
	}

	return nil, errors.WithStack(errors.Newf("interface %q not found in %s", ifaceName, filepath.Clean(dir)))
}

func (p *parserImpl) buildInterface(pkgName, ifaceName string, iface *ast.InterfaceType) (*Interface, error) {
	result := &Interface{
		Name:        ifaceName,
		PackageName: pkgName,
	}

	for _, method := range iface.Methods.List {
		if len(method.Names) == 0 {
			continue // embedded interface
		}
		funcType, ok := method.Type.(*ast.FuncType)
		if !ok {
			continue
		}

		name := method.Names[0].Name
		sql, kind, err := p.extractSQL(method.Doc)
		if err != nil {
			return nil, errors.Wrapf(err, "method %s", name)
		}
		if sql == "" {
			continue // no SQL comment, skip
		}

		params, err := p.buildParams(funcType.Params)
		if err != nil {
			return nil, errors.Wrapf(err, "method %s params", name)
		}

		retKind, retType, retIsPtr, err := p.buildReturn(funcType.Results)
		if err != nil {
			return nil, errors.Wrapf(err, "method %s return", name)
		}

		result.Methods = append(result.Methods, Method{
			Name:        name,
			SQL:         sql,
			QueryKind:   kind,
			Params:      params,
			ReturnKind:  retKind,
			ReturnType:  retType,
			ReturnIsPtr: retIsPtr,
		})
	}

	return result, nil
}

// usedImports returns only the import paths from file that are actually referenced by
// iface's method params/return types, plus blank ("_") and dot (".") imports (kept
// unconditionally, since they're used for their side effects / unqualified names rather
// than through a package-qualified identifier). Copying every import in the source file
// regardless of use would make the generated file fail to compile as soon as that file
// contains any import the interface's own types don't need.
func (p *parserImpl) usedImports(file *ast.File, iface *Interface) []Import {
	used := map[string]struct{}{}
	addType := func(t string) {
		t = strings.TrimLeft(t, "*[]")
		if idx := strings.Index(t, "."); idx > 0 {
			used[t[:idx]] = struct{}{}
		}
	}
	for _, m := range iface.Methods {
		for _, prm := range m.Params {
			addType(prm.Type)
		}
		addType(m.ReturnType)
	}

	var imports []Import
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		switch {
		case imp.Name != nil && (imp.Name.Name == "_" || imp.Name.Name == "."):
			imports = append(imports, Import{Path: path, Alias: imp.Name.Name})
		case imp.Name != nil:
			// Preserve the alias: a type referenced as "alias.Type" only compiles in the
			// generated file if the import is re-emitted with that same alias, since it
			// won't generally match the imported package's own default name.
			if _, ok := used[imp.Name.Name]; ok {
				imports = append(imports, Import{Path: path, Alias: imp.Name.Name})
			}
		default:
			if _, ok := used[defaultImportName(path)]; ok {
				imports = append(imports, Import{Path: path})
			}
		}
	}
	return imports
}

// defaultImportName guesses the package identifier an unaliased import is referenced by,
// from its path's last segment, dropping a trailing major-version marker like "v3" — as a
// path segment ("github.com/foo/bar/v3") or dot-suffixed ("gopkg.in/yaml.v3") — since Go
// package names conventionally drop it too. This is a heuristic: it can't know a package's
// actual declared name without parsing that package, but it covers the common conventions.
func defaultImportName(path string) string {
	segs := strings.Split(path, "/")
	name := segs[len(segs)-1]
	if len(segs) > 1 && isVersionSegment(name) {
		name = segs[len(segs)-2]
	}
	if idx := strings.LastIndex(name, "."); idx > 0 && isVersionSegment(name[idx+1:]) {
		name = name[:idx]
	}
	return name
}

func isVersionSegment(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (p *parserImpl) extractSQL(doc *ast.CommentGroup) (string, QueryKind, error) {
	if doc == nil {
		return "", "", nil
	}

	lines := make([]string, 0, len(doc.List))
	inSQL := false

	for _, c := range doc.List {
		text := strings.TrimPrefix(c.Text, "//")
		text = strings.TrimPrefix(text, " ")

		if !inSQL {
			idx := strings.Index(text, sqlTag)
			if idx == -1 {
				continue // still a regular comment, not SQL yet
			}
			inSQL = true
			text = strings.TrimSpace(text[idx+len(sqlTag):])
			if text == "" {
				continue // @sql tag with nothing after it on this line
			}
		}
		lines = append(lines, text)
	}

	if len(lines) == 0 {
		return "", "", nil
	}

	sql := strings.Join(lines, "\n")
	upper := strings.ToUpper(strings.TrimSpace(lines[0]))

	var kind QueryKind
	for _, kw := range sqlKeywords {
		if strings.HasPrefix(upper, kw) {
			kind = QueryKind(kw)
			break
		}
	}

	return sql, kind, nil
}

func (p *parserImpl) buildParams(fields *ast.FieldList) ([]Param, error) {
	if fields == nil {
		return nil, nil
	}
	var params []Param
	for _, field := range fields.List {
		typeStr := p.exprToString(field.Type)
		if typeStr == "" {
			return nil, errors.WithStack(errors.Newf(
				"unsupported parameter type %q: interface{}/any-underlying, variadic, func, channel, and generic "+
					"types are not supported", paramFieldLabel(field)))
		}
		if len(field.Names) == 0 {
			return nil, errors.WithStack(errors.Newf(
				"unsupported parameter of type %q: every parameter must be named (an unnamed parameter can't be "+
					"bound via :name, and produces an invalid generated method signature)", typeStr))
		}
		isPtr := strings.HasPrefix(typeStr, "*")
		for _, name := range field.Names {
			params = append(params, Param{
				Name:  name.Name,
				Type:  typeStr,
				IsPtr: isPtr,
			})
		}
	}
	return params, nil
}

// paramFieldLabel names a param field for an error message: its declared name(s) if any,
// otherwise "<unnamed>" for an unnamed parameter.
func paramFieldLabel(field *ast.Field) string {
	if len(field.Names) == 0 {
		return "<unnamed>"
	}
	names := make([]string, len(field.Names))
	for i, n := range field.Names {
		names[i] = n.Name
	}
	return strings.Join(names, ", ")
}

// buildReturn maps a method's return signature to a ReturnKind. Only the shapes documented
// in the return-type table are accepted ((*T, error), ([]*T, error), ([]T, error), (int, error),
// error, or no return values); anything else is rejected rather than silently coerced, since
// the generator always renders the return type it decides on (e.g. always "*T" for
// ReturnSingle) regardless of what the source interface actually declared, and a mismatch
// there would only surface later as a confusing compile error where the struct is assigned to
// the interface. The returned bool is only meaningful for ReturnSlice (true for []*T, false
// for []T); ReturnSingle is always pointer (*T is the only supported shape there).
func (p *parserImpl) buildReturn(fields *ast.FieldList) (ReturnKind, string, bool, error) {
	if fields == nil || len(fields.List) == 0 {
		return ReturnNothing, "", false, nil
	}

	types := make([]string, 0, len(fields.List))
	for i, f := range fields.List {
		t := p.exprToString(f.Type)
		if t == "" {
			return "", "", false, errors.WithStack(errors.Newf(
				"unsupported return type at position %d: interface{}/any-underlying, func, channel, and generic "+
					"types are not supported", i+1))
		}
		types = append(types, t)
	}

	switch len(types) {
	case 1:
		if types[0] == "error" {
			return ReturnError, "", false, nil
		}
		return "", "", false, errors.WithStack(errors.Newf(
			"unsupported return type %q: a single return value must be error", types[0]))

	case 2:
		if types[1] != "error" {
			return "", "", false, errors.WithStack(errors.Newf(
				"unsupported return signature (%s, %s): second return value must be error", types[0], types[1]))
		}
		t := types[0]
		switch {
		case t == "int":
			return ReturnRowsOrID, "int", false, nil
		case strings.HasPrefix(t, "[]*"):
			return ReturnSlice, strings.TrimPrefix(t, "[]*"), true, nil
		case strings.HasPrefix(t, "[]"):
			return ReturnSlice, strings.TrimPrefix(t, "[]"), false, nil
		case strings.HasPrefix(t, "*"):
			return ReturnSingle, strings.TrimPrefix(t, "*"), true, nil
		default:
			return "", "", false, errors.WithStack(errors.Newf(
				"unsupported return type %q: expected int, *T, []*T, or []T", t))
		}

	default:
		return "", "", false, errors.WithStack(errors.Newf(
			"unsupported return signature with %d return values", len(types)))
	}
}

func (p *parserImpl) exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + p.exprToString(e.X)
	case *ast.ArrayType:
		return "[]" + p.exprToString(e.Elt)
	case *ast.SelectorExpr:
		return p.exprToString(e.X) + "." + e.Sel.Name
	case *ast.MapType:
		return "map[" + p.exprToString(e.Key) + "]" + p.exprToString(e.Value)
	default:
		return ""
	}
}
