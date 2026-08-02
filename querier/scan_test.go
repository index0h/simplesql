package querier

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

type scanTestUser struct {
	ID   int    `db:"id"`
	Name string `db:"name"`
}

type scanTestBalance struct {
	UserID int `db:"user_id"`
	Amount int `db:"amount"`
}

// scanTestUserBalance mirrors a join result: a struct that embeds two entities,
// each scanned from its own set of columns.
type scanTestUserBalance struct {
	scanTestUser
	scanTestBalance
}

func TestFieldPointers_FlatStruct(t *testing.T) {
	var dest scanTestUser
	ptrs, err := FieldPointers(&dest, []string{"name", "id"})
	require.NoError(t, err)
	require.Len(t, ptrs, 2)

	*ptrs[0].(*string) = "Alice"
	*ptrs[1].(*int) = 42

	require.Equal(t, scanTestUser{ID: 42, Name: "Alice"}, dest)
}

func TestFieldPointers_EmbeddedStructs(t *testing.T) {
	var dest scanTestUserBalance
	ptrs, err := FieldPointers(&dest, []string{"id", "name", "user_id", "amount"})
	require.NoError(t, err)
	require.Len(t, ptrs, 4)

	*ptrs[0].(*int) = 1
	*ptrs[1].(*string) = "Alice"
	*ptrs[2].(*int) = 1
	*ptrs[3].(*int) = 500

	require.Equal(t, scanTestUserBalance{
		scanTestUser:    scanTestUser{ID: 1, Name: "Alice"},
		scanTestBalance: scanTestBalance{UserID: 1, Amount: 500},
	}, dest)
}

func TestFieldPointers_UnknownColumnIsDiscarded(t *testing.T) {
	var dest scanTestUser
	ptrs, err := FieldPointers(&dest, []string{"id", "unknown"})
	require.NoError(t, err)
	require.Len(t, ptrs, 2)

	*ptrs[0].(*int) = 7
	*ptrs[1].(*any) = "ignored"

	require.Equal(t, scanTestUser{ID: 7}, dest)
}

func TestFieldPointers_RejectsNonStructPointer(t *testing.T) {
	var dest int
	_, err := FieldPointers(&dest, []string{"id"})
	require.Error(t, err)
}

func TestFieldPointers_RejectsNonPointer(t *testing.T) {
	dest := scanTestUser{}
	_, err := FieldPointers(dest, []string{"id"})
	require.Error(t, err)
}

// newQueryRows opens a throwaway *sql.Rows via sqlmock, for tests that only care about
// ScanRows validating dest, not about the actual row data.
func newQueryRows(t *testing.T) *sql.Rows {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	rows, err := db.Query("SELECT id FROM users")
	require.NoError(t, err)
	return rows
}

// TestScanRows_RejectsInvalidDest guards against a regression where ScanRows assumed dest
// was always a valid *[]T/*[]*T and panicked (via reflect.Value.Elem()/Type() on an
// invalid Value) on anything else, instead of returning a descriptive error the way
// FieldPointers already does.
func TestScanRows_RejectsInvalidDest(t *testing.T) {
	cases := map[string]any{
		"non-pointer":                []scanTestUser{},
		"nil pointer":                (*[]scanTestUser)(nil),
		"pointer to non-slice":       &scanTestUser{},
		"pointer to non-struct elem": &[]int{},
	}
	for name, dest := range cases {
		t.Run(name, func(t *testing.T) {
			err := ScanRows(newQueryRows(t), dest)
			require.Error(t, err)
		})
	}
}

// TestScanRows_JoinsCloseError guards against a regression where rows.Close()'s error was
// discarded by a bare "defer rows.Close()" — it must now surface, joined onto whatever
// ScanRows is already returning (or standalone if scanning itself succeeded).
func TestScanRows_JoinsCloseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	closeErr := errors.New("close failed")
	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "Alice").CloseError(closeErr)
	mock.ExpectQuery(".*").WillReturnRows(rows)

	sqlRows, err := db.Query("SELECT id, name FROM users")
	require.NoError(t, err)

	var dest []scanTestUser
	err = ScanRows(sqlRows, &dest)
	require.ErrorIs(t, err, closeErr)
	// The scan itself succeeded; the close error must not have clobbered the result.
	require.Equal(t, []scanTestUser{{ID: 1, Name: "Alice"}}, dest)
}

// TestScanRows_JoinsCloseErrorWithScanError covers the case that actually distinguishes
// errors.Join from errors.CombineErrors: an existing (scan) error already set, with a
// close error on top. CombineErrors would have silently dropped the close error here
// (it attaches it as a "secondary" that Error()/errors.Is never surface); Join must not.
func TestScanRows_JoinsCloseErrorWithScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	closeErr := errors.New("close failed")
	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow("not-a-number", "Alice"). // fails to scan into the int ID field
		CloseError(closeErr)
	mock.ExpectQuery(".*").WillReturnRows(rows)

	sqlRows, err := db.Query("SELECT id, name FROM users")
	require.NoError(t, err)

	var dest []scanTestUser
	err = ScanRows(sqlRows, &dest)
	require.Error(t, err)
	require.ErrorIs(t, err, closeErr, "close error must be discoverable even alongside a scan error")
	require.Contains(t, err.Error(), "not-a-number")
}
