package querier

import (
	"testing"

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
