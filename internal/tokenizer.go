package internal

import (
	"strings"

	"github.com/cockroachdb/errors"
)

type tokKind int

const (
	tokText   tokKind = iota
	tokIf             // {{if <expr>}}
	tokElseIf         // {{else if <expr>}}
	tokElse           // {{else}}
	tokEnd            // {{end}}
	tokSubst          // {{param}}
)

type tok struct {
	kind tokKind
	text string // expression for if/elseif, or param name for subst
}

// Tokenizer splits a SQL template string into a sequence of tokens.
type Tokenizer struct{}

// NewTokenizer returns a Tokenizer.
func NewTokenizer() *Tokenizer {
	return &Tokenizer{}
}

func (t *Tokenizer) tokenize(sql string) ([]tok, error) {
	var tokens []tok
	for len(sql) > 0 {
		open := strings.Index(sql, "{{")
		if open < 0 {
			tokens = append(tokens, tok{kind: tokText, text: sql})
			break
		}
		if open > 0 {
			tokens = append(tokens, tok{kind: tokText, text: sql[:open]})
		}
		sql = sql[open+2:]
		closeIdx := strings.Index(sql, "}}")
		if closeIdx < 0 {
			return nil, errors.WithStack(errors.New("unclosed {{"))
		}
		inner := strings.TrimSpace(sql[:closeIdx])
		sql = sql[closeIdx+2:]
		tk, err := t.classifyAction(inner)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tk)
	}
	return tokens, nil
}

func (t *Tokenizer) classifyAction(s string) (tok, error) {
	switch {
	case s == "end":
		return tok{kind: tokEnd}, nil
	case s == "else":
		return tok{kind: tokElse}, nil
	case strings.HasPrefix(s, "else if "):
		return tok{kind: tokElseIf, text: strings.TrimSpace(s[len("else if"):])}, nil
	case strings.HasPrefix(s, "if "):
		return tok{kind: tokIf, text: strings.TrimSpace(s[len("if"):])}, nil
	default:
		if t.isIdent(s) {
			return tok{kind: tokSubst, text: s}, nil
		}
		return tok{}, errors.WithStack(errors.Newf("unrecognised action: %q", s))
	}
}

func (t *Tokenizer) isIdent(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, r := range s {
		if i == 0 && !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
		if i > 0 && !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
