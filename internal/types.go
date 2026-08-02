package internal

// QueryKind is the SQL statement type detected from the first keyword.
type QueryKind string

const (
	QuerySelect QueryKind = "SELECT"
	QueryInsert QueryKind = "INSERT"
	QueryUpdate QueryKind = "UPDATE"
	QueryDelete QueryKind = "DELETE"
)

// Param is a method parameter.
type Param struct {
	Name  string
	Type  string // Go type expression, e.g. "*bool", "string", "[]*User"
	IsPtr bool
}

// ReturnKind describes what the method returns.
type ReturnKind string

const (
	ReturnNothing  ReturnKind = "nothing"    // ()
	ReturnError    ReturnKind = "error"      // (error)
	ReturnSingle   ReturnKind = "single"     // (*T, error)
	ReturnSlice    ReturnKind = "slice"      // ([]*T, error) or ([]T, error)
	ReturnRowsOrID ReturnKind = "rows_or_id" // (int, error)
)

// Method holds everything the generator needs about one interface method.
type Method struct {
	Name        string
	SQL         string // raw SQL template text extracted from comments
	QueryKind   QueryKind
	Params      []Param
	ReturnKind  ReturnKind
	ReturnType  string // element type for single/slice, e.g. "User" (the "*"/"[]" markers are stripped)
	ReturnIsPtr bool   // for ReturnSlice: true for []*T, false for []T. Always true for ReturnSingle (*T is the only supported shape).
}

// Import is a Go import referenced by a parsed interface's param/return types: a path with
// an optional local name — "" for a normal (unaliased) import, "_" for blank, "." for dot,
// or any other identifier for an explicit alias.
type Import struct {
	Path  string
	Alias string
}

// Interface holds the parsed interface.
type Interface struct {
	Name        string
	PackageName string
	Imports     []Import
	Methods     []Method
}
