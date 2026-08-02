//go:build functional

package repo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/index0h/simplesql/querier"
)

func mysqlDSN() string {
	if v := os.Getenv("MYSQL_DSN"); v != "" {
		return v
	}
	return "simplesql:simplesql@tcp(127.0.0.1:33066)/simplesql?parseTime=true"
}

// newMySQLRepo opens a fresh connection, truncates the fixture tables and returns
// the raw *sql.DB (for seeding/assertions), its ConnectionManager (for transaction
// tests) and the generated repository.
func newMySQLRepo(t *testing.T) (*sql.DB, *querier.ConnectionManager, UserRepository) {
	t.Helper()

	db, err := sql.Open("mysql", mysqlDSN())
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("DELETE FROM balances")
	require.NoError(t, err)
	_, err = db.Exec("DELETE FROM users")
	require.NoError(t, err)

	cm := querier.NewConnectionManager(db)
	return db, cm, NewUserRepository(cm)
}

func seedMySQLUser(t *testing.T, db *sql.DB, name string, enabled bool) int {
	t.Helper()
	res, err := db.Exec("INSERT INTO users (name, enabled) VALUES (?, ?)", name, enabled)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return int(id)
}

func seedMySQLBalance(t *testing.T, db *sql.DB, userID, amount int) {
	t.Helper()
	_, err := db.Exec("INSERT INTO balances (user_id, amount) VALUES (?, ?)", userID, amount)
	require.NoError(t, err)
}

func TestMySQL_FindById(t *testing.T) {
	ctx := context.Background()
	db, _, r := newMySQLRepo(t)
	id := seedMySQLUser(t, db, "Alice", true)
	seedMySQLBalance(t, db, id, 100)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Alice", got.Name)
	require.True(t, got.Enabled)

	missing, err := r.FindById(ctx, id+999)
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestMySQL_FindWithBalance(t *testing.T) {
	ctx := context.Background()
	db, _, r := newMySQLRepo(t)
	id := seedMySQLUser(t, db, "Alice", true)
	seedMySQLBalance(t, db, id, 500)

	got, err := r.FindWithBalance(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Alice", got.Name)
	require.True(t, got.Enabled)
	require.Equal(t, id, got.UserID)
	require.Equal(t, 500, got.Amount)
}

func TestMySQL_ListWithBalances(t *testing.T) {
	ctx := context.Background()
	db, _, r := newMySQLRepo(t)
	id1 := seedMySQLUser(t, db, "Alice", true)
	seedMySQLBalance(t, db, id1, 500)
	id2 := seedMySQLUser(t, db, "Bob", false)
	seedMySQLBalance(t, db, id2, 250)
	seedMySQLUser(t, db, "Carl", true)
	// Carl has no balance row; the INNER JOIN must exclude him.

	got, err := r.ListWithBalances(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "Alice", got[0].Name)
	require.Equal(t, id1, got[0].UserID)
	require.Equal(t, 500, got[0].Amount)
	require.Equal(t, "Bob", got[1].Name)
	require.Equal(t, id2, got[1].UserID)
	require.Equal(t, 250, got[1].Amount)
}

func TestMySQL_FindAll(t *testing.T) {
	ctx := context.Background()
	db, _, r := newMySQLRepo(t)
	seedMySQLUser(t, db, "Alice", true)
	id2 := seedMySQLUser(t, db, "Bob", false)
	id3 := seedMySQLUser(t, db, "Carl", true)

	all, err := r.FindAll(ctx, nil, nil)
	require.NoError(t, err)
	require.Len(t, all, 3)

	enabled := true
	onlyEnabled, err := r.FindAll(ctx, &enabled, nil)
	require.NoError(t, err)
	require.Len(t, onlyEnabled, 2)

	minID := id2
	onlyEnabledAboveMin, err := r.FindAll(ctx, &enabled, &minID)
	require.NoError(t, err)
	require.Len(t, onlyEnabledAboveMin, 1)
	require.Equal(t, "Carl", onlyEnabledAboveMin[0].Name)

	onlyAboveMin, err := r.FindAll(ctx, nil, &minID)
	require.NoError(t, err)
	require.Len(t, onlyAboveMin, 1)
	require.Equal(t, id3, onlyAboveMin[0].ID)
}

func TestMySQL_SelectColumns(t *testing.T) {
	ctx := context.Background()
	db, _, r := newMySQLRepo(t)
	seedMySQLUser(t, db, "Alice", true)

	rows, err := r.SelectColumns(ctx, "id, name, enabled")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Alice", rows[0].Name)
}

func TestMySQL_Create(t *testing.T) {
	ctx := context.Background()
	_, _, r := newMySQLRepo(t)

	id, err := r.Create(ctx, "Dave", true)
	require.NoError(t, err)
	require.Positive(t, id)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Dave", got.Name)
	require.True(t, got.Enabled)
}

func TestMySQL_UpdateEnabled(t *testing.T) {
	ctx := context.Background()
	db, _, r := newMySQLRepo(t)
	id := seedMySQLUser(t, db, "Alice", false)

	n, err := r.UpdateEnabled(ctx, id, true)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.True(t, got.Enabled)
}

func TestMySQL_DeleteById(t *testing.T) {
	ctx := context.Background()
	db, _, r := newMySQLRepo(t)
	id := seedMySQLUser(t, db, "Alice", true)

	n, err := r.DeleteById(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestMySQL_Rename(t *testing.T) {
	ctx := context.Background()
	db, _, r := newMySQLRepo(t)
	id := seedMySQLUser(t, db, "Alice", true)

	require.NoError(t, r.Rename(ctx, id, "Alicia"))

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Alicia", got.Name)
}

func TestMySQL_FindByIdSync(t *testing.T) {
	db, _, r := newMySQLRepo(t)
	id := seedMySQLUser(t, db, "Alice", true)

	got, err := r.FindByIdSync(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Alice", got.Name)
}

func TestMySQL_Transaction(t *testing.T) {
	ctx := context.Background()
	_, cm, r := newMySQLRepo(t)

	var committedID int
	err := cm.StartTransaction(ctx, func(ctx context.Context) error {
		var err error
		committedID, err = r.Create(ctx, "Alice", true)
		return err
	})
	require.NoError(t, err)
	got, err := r.FindById(ctx, committedID)
	require.NoError(t, err)
	require.NotNil(t, got)

	boom := errors.New("boom")
	var rolledBackID int
	err = cm.StartTransaction(ctx, func(ctx context.Context) error {
		var err error
		rolledBackID, err = r.Create(ctx, "Bob", true)
		if err != nil {
			return err
		}
		return boom
	})
	require.ErrorIs(t, err, boom)

	missing, err := r.FindById(ctx, rolledBackID)
	require.NoError(t, err)
	require.Nil(t, missing)
}
