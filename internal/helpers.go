package internal

import (
	"strconv"
	"strings"
	"text/template"
)

var helperFuncs = template.FuncMap{
	"paramList": paramList,
	"quoteSQL":  strconv.Quote,
}

func paramList(params []Param) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.Name + " " + p.Type
	}
	return strings.Join(parts, ", ")
}
