package internal

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
)

// namedParamRe matches either a literal "::" (Postgres type cast, e.g. "amount::numeric")
// or a bound param ":name". The "::" alternative must come first so it wins the match at
// that position; callers must check whether the capture group participated before treating
// a match as a bound param.
var namedParamRe = regexp.MustCompile(`::|:([a-zA-Z_][a-zA-Z0-9_]*)`)

type compilerImpl struct {
	dialect   Dialect
	tokenizer *Tokenizer
}

// NewCompiler returns a Compiler for the given dialect.
func NewCompiler(dialect Dialect) Compiler {
	return &compilerImpl{dialect: dialect, tokenizer: NewTokenizer()}
}

// Dialect returns the dialect this Compiler was constructed for.
func (c *compilerImpl) Dialect() Dialect {
	return c.dialect
}

type bodyCompiler struct {
	out          strings.Builder
	tokens       []tok
	pos          int
	params       map[string]Param
	dialect      Dialect
	needsStrconv bool
}

// CompileBody compiles a SQL template string into a Go source fragment
// that builds _query (string) and _args ([]any).
func (c *compilerImpl) CompileBody(m *Method) (body string, needsStrconv bool, err error) {
	tokens, err := c.tokenizer.tokenize(m.SQL)
	if err != nil {
		return "", false, errors.Wrapf(err, "tokenize SQL in %s", m.Name)
	}

	paramMap := make(map[string]Param, len(m.Params))
	for _, p := range m.Params {
		paramMap[p.Name] = p
	}

	bc := &bodyCompiler{
		tokens:  tokens,
		params:  paramMap,
		dialect: c.dialect,
	}

	bc.out.WriteString("\tvar _sb strings.Builder\n")
	bc.out.WriteString("\tvar _args []any\n")

	err = bc.walk()
	if err != nil {
		return "", false, errors.Wrapf(err, "compile SQL in %s", m.Name)
	}
	if bc.pos < len(bc.tokens) {
		return "", false, errors.WithStack(errors.Newf("unexpected token %q in %s", bc.tokens[bc.pos].text, m.Name))
	}

	// Postgres has no LastInsertId() equivalent (there's no wire-protocol call for it,
	// only a RETURNING clause), so an INSERT method that reports back the new row's id
	// needs "RETURNING id" appended to the query; the generator pairs this with a
	// QueryRowContext+Scan template instead of ExecContext+LastInsertId (see
	// generator.go's renderMethod). Skip it if the template already has its own
	// RETURNING clause, so we don't emit an invalid doubled clause.
	if c.dialect == DialectPostgres && m.QueryKind == QueryInsert && m.ReturnKind == ReturnRowsOrID &&
		!strings.Contains(strings.ToUpper(m.SQL), "RETURNING") {
		bc.out.WriteString("\t_sb.WriteString(\" RETURNING id\")\n")
	}

	bc.out.WriteString("\t_query := _sb.String()\n")
	return bc.out.String(), bc.needsStrconv, nil
}

func (c *bodyCompiler) walk() error {
	for c.pos < len(c.tokens) {
		t := c.tokens[c.pos]
		switch t.kind {
		case tokEnd, tokElse, tokElseIf:
			return nil // caller handles
		case tokText:
			c.pos++
			c.emitText(t.text)
		case tokIf:
			c.pos++
			err := c.emitIfChain(t.text)
			if err != nil {
				return err
			}
		case tokSubst:
			c.pos++
			err := c.emitDirect(t.text)
			if err != nil {
				return err
			}
		default:
			return errors.WithStack(errors.Newf("unexpected token kind %d", t.kind))
		}
	}
	return nil
}

func (c *bodyCompiler) emitIfChain(expr string) error {
	c.out.WriteString(fmt.Sprintf("\tif %s {\n", expr))
	err := c.walk()
	if err != nil {
		return err
	}
	for c.pos < len(c.tokens) {
		t := c.tokens[c.pos]
		c.pos++
		switch t.kind {
		case tokEnd:
			c.out.WriteString("\t}\n")
			return nil
		case tokElse:
			c.out.WriteString("\t} else {\n")
			err := c.walk()
			if err != nil {
				return err
			}
			// expect end
			if c.pos >= len(c.tokens) || c.tokens[c.pos].kind != tokEnd {
				return errors.WithStack(errors.New("expected {{end}} after {{else}}"))
			}
			c.pos++
			c.out.WriteString("\t}\n")
			return nil
		case tokElseIf:
			c.out.WriteString(fmt.Sprintf("\t} else if %s {\n", t.text))
			err := c.walk()
			if err != nil {
				return err
			}
		default:
			return errors.WithStack(errors.Newf("expected {{end}}/{{else}}/{{else if}}, got %q", t.text))
		}
	}
	return errors.WithStack(errors.New("missing {{end}}"))
}

func (c *bodyCompiler) emitText(text string) {
	// For MySQL: fold each "?" directly into the surrounding string literal.
	// For Postgres: emit placeholder as a runtime expression.
	if c.dialect == DialectPostgres {
		c.emitTextPostgres(text)
	} else {
		// MySQL and SQLite both use ? placeholders.
		c.emitTextMySQL(text)
	}
}

func (c *bodyCompiler) emitTextMySQL(text string) {
	matches := namedParamRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		if text != "" {
			c.out.WriteString(fmt.Sprintf("\t_sb.WriteString(%s)\n", strconv.Quote(text)))
		}
		return
	}
	// Build the full string with ? in place, then emit _args appends in order.
	// This collapses adjacent literals and placeholders into one WriteString call.
	var sb strings.Builder
	var paramNames []string
	last := 0
	for _, m := range matches {
		sb.WriteString(text[last:m[0]])
		if m[2] < 0 {
			sb.WriteString(text[m[0]:m[1]]) // literal "::", not a bound param
		} else {
			sb.WriteString("?")
			paramNames = append(paramNames, text[m[2]:m[3]])
		}
		last = m[1]
	}
	sb.WriteString(text[last:])
	c.out.WriteString(fmt.Sprintf("\t_sb.WriteString(%s)\n", strconv.Quote(sb.String())))
	for _, name := range paramNames {
		c.out.WriteString(fmt.Sprintf("\t_args = append(_args, %s)\n", name))
	}
}

func (c *bodyCompiler) emitTextPostgres(text string) {
	matches := namedParamRe.FindAllStringSubmatchIndex(text, -1)
	last := 0
	for _, m := range matches {
		if m[0] > last {
			c.out.WriteString(fmt.Sprintf("\t_sb.WriteString(%s)\n", strconv.Quote(text[last:m[0]])))
		}
		if m[2] < 0 {
			c.out.WriteString(fmt.Sprintf("\t_sb.WriteString(%s)\n", strconv.Quote(text[m[0]:m[1]]))) // literal "::"
		} else {
			c.needsStrconv = true
			c.out.WriteString("\t_sb.WriteString(\"$\" + strconv.Itoa(len(_args)+1))\n")
			c.out.WriteString(fmt.Sprintf("\t_args = append(_args, %s)\n", text[m[2]:m[3]]))
		}
		last = m[1]
	}
	if last < len(text) {
		c.out.WriteString(fmt.Sprintf("\t_sb.WriteString(%s)\n", strconv.Quote(text[last:])))
	}
}

func (c *bodyCompiler) emitDirect(paramName string) error {
	p, ok := c.params[paramName]
	if !ok {
		return errors.WithStack(errors.Newf("unknown param %q in direct substitution", paramName))
	}
	switch p.Type {
	case "*string":
		c.out.WriteString(fmt.Sprintf("\tif %s != nil { _sb.WriteString(*%s) }\n", paramName, paramName))
	case "string":
		c.out.WriteString(fmt.Sprintf("\t_sb.WriteString(%s)\n", paramName))
	default:
		// Only string/*string are documented as supported for {{param}}. A silent
		// int64(...) fallback here would (a) generate uncompilable code for anything
		// that isn't numeric (bool, time.Time, structs, ...) and (b) quietly accept a
		// wider surface than what's documented, for types it happens to compile for.
		return errors.WithStack(errors.Newf(
			"unsupported type %q for direct substitution {{%s}}: must be string or *string", p.Type, paramName))
	}
	return nil
}
