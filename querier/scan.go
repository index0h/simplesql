package querier

import (
	"database/sql"
	"reflect"
	"strings"

	"github.com/cockroachdb/errors"
)

// ScanRows scans all rows from *sql.Rows into dest (*[]*T or *[]T).
func ScanRows(rows *sql.Rows, dest any) error {
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return errors.WithStack(err)
	}

	sliceVal := reflect.ValueOf(dest).Elem()
	elemType := sliceVal.Type().Elem()
	isPtr := elemType.Kind() == reflect.Ptr
	structType := elemType
	if isPtr {
		structType = elemType.Elem()
	}

	// Column list and struct type are fixed for the whole result set, so the tag index
	// (including the recursive walk over any embedded structs) only needs building once.
	index := buildTagIndex(structType)

	for rows.Next() {
		elem := reflect.New(structType)
		err = rows.Scan(fieldPointersFromIndex(elem, cols, index)...)
		if err != nil {
			return errors.WithStack(err)
		}
		if isPtr {
			sliceVal.Set(reflect.Append(sliceVal, elem))
		} else {
			sliceVal.Set(reflect.Append(sliceVal, elem.Elem()))
		}
	}
	return errors.WithStack(rows.Err())
}

// FieldPointers returns scan destination pointers for dest (pointer to struct)
// ordered to match cols from rows.Columns().
func FieldPointers(dest any, cols []string) ([]any, error) {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return nil, errors.WithStack(errors.New("dest must be a pointer to struct"))
	}
	index := buildTagIndex(v.Elem().Type())
	return fieldPointersFromIndex(v, cols, index), nil
}

// fieldPointersFromIndex returns scan destination pointers for structPtr (a pointer to
// struct) ordered to match cols, using a tag index already built for structPtr's type.
func fieldPointersFromIndex(structPtr reflect.Value, cols []string, index map[string][]int) []any {
	ptrs := make([]any, len(cols))
	for i, col := range cols {
		fieldIdx, ok := index[col]
		if !ok {
			var discard any
			ptrs[i] = &discard
			continue
		}
		ptrs[i] = structPtr.Elem().FieldByIndex(fieldIdx).Addr().Interface()
	}
	return ptrs
}

// buildTagIndex maps db column name to a field index path (per reflect.Value.FieldByIndex),
// recursing into anonymous embedded structs so join results can be scanned into a struct
// that embeds multiple entities, e.g. struct { User; Balance }.
func buildTagIndex(t reflect.Type) map[string][]int {
	m := make(map[string][]int, t.NumField())
	collectTagIndex(t, nil, m)
	return m
}

func collectTagIndex(t reflect.Type, prefix []int, m map[string][]int) {
	for i := range t.NumField() {
		f := t.Field(i)
		idx := append(append([]int{}, prefix...), i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			collectTagIndex(f.Type, idx, m)
			continue
		}
		tag := f.Tag.Get("db")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.SplitN(tag, ",", 2)[0]
		m[name] = idx
	}
}
