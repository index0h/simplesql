//go:build functional

package repo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/require"

	"github.com/index0h/simplesql/querier"
)

// newSQLiteRepo creates a fresh on-disk SQLite database (SQLite has no server to
// dockerize, so unlike mysql/postgres this fixture is created in-process), applies
// the schema and returns the *sql.DB, its ConnectionManager and the generated repository.
func newSQLiteRepo(t *testing.T) (*sql.DB, *querier.ConnectionManager, UserRepository) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "functional.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	// modernc.org/sqlite serializes access at the connection-pool level; a single
	// open connection avoids "database is locked" errors from concurrent writers.
	db.SetMaxOpenConns(1)

	schema, err := os.ReadFile(filepath.Join("..", "schema", "sqlite.sql"))
	require.NoError(t, err)
	for _, stmt := range strings.Split(string(schema), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		_, err := db.Exec(stmt)
		require.NoError(t, err)
	}

	cm := querier.NewConnectionManager(db)
	return db, cm, NewUserRepository(cm)
}

func seedSQLiteUser(t *testing.T, db *sql.DB, name string, enabled bool) int {
	t.Helper()
	res, err := db.Exec("INSERT INTO users (name, enabled) VALUES (?, ?)", name, enabled)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return int(id)
}

func seedSQLiteBalance(t *testing.T, db *sql.DB, userID, amount int) {
	t.Helper()
	_, err := db.Exec("INSERT INTO balances (user_id, amount) VALUES (?, ?)", userID, amount)
	require.NoError(t, err)
}

func TestSQLite_FindById(t *testing.T) {
	ctx := context.Background()
	db, _, r := newSQLiteRepo(t)
	id := seedSQLiteUser(t, db, "Alice", true)
	seedSQLiteBalance(t, db, id, 100)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Alice", got.Name)
	require.True(t, got.Enabled)

	missing, err := r.FindById(ctx, id+999)
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestSQLite_FindWithBalance(t *testing.T) {
	ctx := context.Background()
	db, _, r := newSQLiteRepo(t)
	id := seedSQLiteUser(t, db, "Alice", true)
	seedSQLiteBalance(t, db, id, 500)

	got, err := r.FindWithBalance(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Alice", got.Name)
	require.True(t, got.Enabled)
	require.Equal(t, id, got.UserID)
	require.Equal(t, 500, got.Amount)
}

func TestSQLite_ListWithBalances(t *testing.T) {
	ctx := context.Background()
	db, _, r := newSQLiteRepo(t)
	id1 := seedSQLiteUser(t, db, "Alice", true)
	seedSQLiteBalance(t, db, id1, 500)
	id2 := seedSQLiteUser(t, db, "Bob", false)
	seedSQLiteBalance(t, db, id2, 250)
	seedSQLiteUser(t, db, "Carl", true)
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

func TestSQLite_FindAll(t *testing.T) {
	ctx := context.Background()
	db, _, r := newSQLiteRepo(t)
	seedSQLiteUser(t, db, "Alice", true)
	id2 := seedSQLiteUser(t, db, "Bob", false)
	id3 := seedSQLiteUser(t, db, "Carl", true)

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

func TestSQLite_SelectColumns(t *testing.T) {
	ctx := context.Background()
	db, _, r := newSQLiteRepo(t)
	seedSQLiteUser(t, db, "Alice", true)

	rows, err := r.SelectColumns(ctx, "id, name, enabled")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Alice", rows[0].Name)
}

func TestSQLite_Create(t *testing.T) {
	ctx := context.Background()
	_, _, r := newSQLiteRepo(t)

	id, err := r.Create(ctx, "Dave", true)
	require.NoError(t, err)
	require.Positive(t, id)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Dave", got.Name)
	require.True(t, got.Enabled)
}

func TestSQLite_UpdateEnabled(t *testing.T) {
	ctx := context.Background()
	db, _, r := newSQLiteRepo(t)
	id := seedSQLiteUser(t, db, "Alice", false)

	n, err := r.UpdateEnabled(ctx, id, true)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.True(t, got.Enabled)
}

func TestSQLite_DeleteById(t *testing.T) {
	ctx := context.Background()
	db, _, r := newSQLiteRepo(t)
	id := seedSQLiteUser(t, db, "Alice", true)

	n, err := r.DeleteById(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestSQLite_Rename(t *testing.T) {
	ctx := context.Background()
	db, _, r := newSQLiteRepo(t)
	id := seedSQLiteUser(t, db, "Alice", true)

	require.NoError(t, r.Rename(ctx, id, "Alicia"))

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Alicia", got.Name)
}

func TestSQLite_FindByIdSync(t *testing.T) {
	db, _, r := newSQLiteRepo(t)
	id := seedSQLiteUser(t, db, "Alice", true)

	got, err := r.FindByIdSync(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Alice", got.Name)
}

func TestSQLite_Transaction(t *testing.T) {
	ctx := context.Background()
	_, cm, r := newSQLiteRepo(t)

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
