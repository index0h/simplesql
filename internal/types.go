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
	Name       string
	SQL        string // raw SQL template text extracted from comments
	QueryKind  QueryKind
	Params     []Param
	ReturnKind ReturnKind
	ReturnType string // element type for single/slice, e.g. "User", "*User"
}

// Interface holds the parsed interface.
type Interface struct {
	Name        string
	PackageName string
	Imports     []string // import paths referenced by param/return types
	Methods     []Method
}
