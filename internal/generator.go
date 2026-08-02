package internal

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"
	"strings"
	"text/template"

	"github.com/cockroachdb/errors"
	"go.uber.org/zap"
)

// Request describes one interface to generate.
type Request struct {
	Interface  *Interface
	StructName string
}

type generatorImpl struct {
	compiler Compiler
	log      *zap.Logger
}

// NewGenerator returns a Generator backed by the given compiler.
func NewGenerator(log *zap.Logger, compiler Compiler) Generator {
	return &generatorImpl{compiler: compiler, log: log}
}

// Generate returns formatted Go source for the given requests placed into one file.
func (g *generatorImpl) Generate(packageName string, requests []*Request) ([]byte, error) {
	// First pass: compile all method bodies to know which extra imports are needed.
	type compiledMethod struct {
		method  Method
		body    string
		ctxExpr string
	}
	type compiledIface struct {
		req     *Request
		methods []compiledMethod
	}

	needsStrconv := false
	compiled := make([]compiledIface, 0, len(requests))

	for _, req := range requests {
		g.log.Debug("generating interface", zap.String("interface", req.Interface.Name))
		ci := compiledIface{req: req}
		for _, m := range req.Interface.Methods {
			g.log.Debug("compiling method", zap.String("method", req.Interface.Name+"."+m.Name))
			ctxExpr, sqlParams := g.extractContext(m.Params)
			mCopy := m
			mCopy.Params = sqlParams
			body, ns, err := g.compiler.CompileBody(&mCopy)
			if err != nil {
				return nil, errors.Wrapf(err, "compile %s.%s", req.Interface.Name, m.Name)
			}
			if ns {
				needsStrconv = true
			}
			ci.methods = append(ci.methods, compiledMethod{method: m, body: body, ctxExpr: ctxExpr})
		}
		compiled = append(compiled, ci)
	}

	var buf bytes.Buffer
	err := headerTmpl.Execute(&buf, map[string]any{
		"Package": packageName,
		"Imports": g.buildImportBlock(requests, needsStrconv),
	})
	if err != nil {
		return nil, errors.Wrap(err, "render header")
	}

	for _, ci := range compiled {
		structName := ci.req.StructName
		if structName == "" {
			structName = g.defaultStructName(ci.req.Interface.Name)
		}
		err := structTmpl.Execute(&buf, map[string]any{
			"InterfaceName": ci.req.Interface.Name,
			"StructName":    structName,
		})
		if err != nil {
			return nil, errors.Wrapf(err, "render struct %s", ci.req.Interface.Name)
		}
		for _, cm := range ci.methods {
			err := g.renderMethod(&buf, structName, cm.method, cm.body, cm.ctxExpr)
			if err != nil {
				return nil, errors.Wrapf(err, "render %s.%s", ci.req.Interface.Name, cm.method.Name)
			}
		}
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		return buf.Bytes(), errors.Wrap(err, "format generated source")
	}
	return src, nil
}

func (g *generatorImpl) renderMethod(buf *bytes.Buffer, structName string, m Method, queryBody, ctxExpr string) error {
	data := &methodData{
		StructName:  structName,
		MethodName:  m.Name,
		Params:      m.Params,
		ReturnKind:  m.ReturnKind,
		ReturnType:  m.ReturnType,
		QueryBody:   queryBody,
		ContextExpr: ctxExpr,
	}

	var tmpl *template.Template
	switch m.ReturnKind {
	case ReturnSingle:
		tmpl = selectOneTmpl
	case ReturnSlice:
		tmpl = selectManyTmpl
	case ReturnRowsOrID:
		switch {
		case m.QueryKind == QueryInsert && g.compiler.Dialect() == DialectPostgres:
			tmpl = insertIDReturningTmpl
		case m.QueryKind == QueryInsert:
			tmpl = insertIDTmpl
		default:
			tmpl = execRowsTmpl
		}
	case ReturnError:
		tmpl = execErrTmpl
	default:
		tmpl = execVoidTmpl
	}

	return errors.Wrap(tmpl.Execute(buf, data), "render method template")
}

type methodData struct {
	StructName  string
	MethodName  string
	Params      []Param
	ReturnKind  ReturnKind
	ReturnType  string
	QueryBody   string
	ContextExpr string // param name (e.g. "ctx") or "context.Background()"
}

func (g *generatorImpl) defaultStructName(ifaceName string) string {
	if len(ifaceName) == 0 {
		return "impl"
	}
	return strings.ToLower(ifaceName[:1]) + ifaceName[1:] + "Impl"
}

// extractContext checks if the first param is context.Context.
// If so, returns its name and the remaining params; otherwise returns "context.Background()" and all params.
func (g *generatorImpl) extractContext(params []Param) (ctxExpr string, sqlParams []Param) {
	if len(params) > 0 && params[0].Type == "context.Context" {
		return params[0].Name, params[1:]
	}
	return "context.Background()", params
}

// buildImportBlock returns a sorted, grouped import block string for use in the header template.
// Stdlib imports come first, then a blank line, then external imports.
func (g *generatorImpl) buildImportBlock(requests []*Request, needsStrconv bool) string {
	set := map[string]struct{}{
		"context":                              {},
		"strings":                              {},
		"github.com/cockroachdb/errors":        {},
		"github.com/index0h/simplesql/querier": {},
	}
	if needsStrconv {
		set["strconv"] = struct{}{}
	}
	for _, req := range requests {
		for _, imp := range req.Interface.Imports {
			set[imp] = struct{}{}
		}
	}

	var std, ext []string
	for imp := range set {
		if g.isStdlib(imp) {
			std = append(std, imp)
		} else {
			ext = append(ext, imp)
		}
	}
	sort.Strings(std)
	sort.Strings(ext)

	var sb strings.Builder
	for _, imp := range std {
		sb.WriteString(fmt.Sprintf("\t%q\n", imp))
	}
	if len(std) > 0 && len(ext) > 0 {
		sb.WriteString("\n")
	}
	for _, imp := range ext {
		sb.WriteString(fmt.Sprintf("\t%q\n", imp))
	}
	return sb.String()
}

// isStdlib reports whether the import path is a standard library package.
// Stdlib paths have no dot in the first path element.
func (g *generatorImpl) isStdlib(path string) bool {
	first := strings.SplitN(path, "/", 2)[0]
	return !strings.Contains(first, ".")
}
