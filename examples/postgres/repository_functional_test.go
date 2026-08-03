//go:build functional

package repo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/index0h/simplesql/simplesql"
)

func postgresDSN() string {
	if v := os.Getenv("POSTGRES_DSN"); v != "" {
		return v
	}
	return "postgres://simplesql:simplesql@127.0.0.1:54322/simplesql?sslmode=disable"
}

// newPostgresRepo opens a fresh connection, truncates the fixture tables and returns
// the raw *sql.DB (for seeding/assertions), its ConnectionManager (for transaction
// tests) and the generated repository.
func newPostgresRepo(t *testing.T) (*sql.DB, *simplesql.ConnectionManager, UserRepository) {
	t.Helper()

	db, err := sql.Open("pgx", postgresDSN())
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("DELETE FROM balances")
	require.NoError(t, err)
	_, err = db.Exec("DELETE FROM users")
	require.NoError(t, err)

	cm := simplesql.NewConnectionManager(db)
	return db, cm, NewUserRepository(cm)
}

// seedPostgresUser seeds a user directly via RETURNING id, independent of the generated
// Create method under test.
func seedPostgresUser(t *testing.T, db *sql.DB, name string, enabled bool) int {
	t.Helper()
	var id int
	err := db.QueryRow("INSERT INTO users (name, enabled) VALUES ($1, $2) RETURNING id", name, enabled).Scan(&id)
	require.NoError(t, err)
	return id
}

func seedPostgresBalance(t *testing.T, db *sql.DB, userID, amount int) {
	t.Helper()
	_, err := db.Exec("INSERT INTO balances (user_id, amount) VALUES ($1, $2)", userID, amount)
	require.NoError(t, err)
}

func TestPostgres_FindById(t *testing.T) {
	ctx := context.Background()
	db, _, r := newPostgresRepo(t)
	id := seedPostgresUser(t, db, "Alice", true)
	seedPostgresBalance(t, db, id, 100)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Alice", got.Name)
	require.True(t, got.Enabled)

	missing, err := r.FindById(ctx, id+999)
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestPostgres_FindWithBalance(t *testing.T) {
	ctx := context.Background()
	db, _, r := newPostgresRepo(t)
	id := seedPostgresUser(t, db, "Alice", true)
	seedPostgresBalance(t, db, id, 500)

	got, err := r.FindWithBalance(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Alice", got.Name)
	require.True(t, got.Enabled)
	require.Equal(t, id, got.UserID)
	require.Equal(t, 500, got.Amount)
}

func TestPostgres_ListWithBalances(t *testing.T) {
	ctx := context.Background()
	db, _, r := newPostgresRepo(t)
	id1 := seedPostgresUser(t, db, "Alice", true)
	seedPostgresBalance(t, db, id1, 500)
	id2 := seedPostgresUser(t, db, "Bob", false)
	seedPostgresBalance(t, db, id2, 250)
	seedPostgresUser(t, db, "Carl", true)
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

func TestPostgres_FindAll(t *testing.T) {
	ctx := context.Background()
	db, _, r := newPostgresRepo(t)
	seedPostgresUser(t, db, "Alice", true)
	id2 := seedPostgresUser(t, db, "Bob", false)
	id3 := seedPostgresUser(t, db, "Carl", true)

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

func TestPostgres_SelectColumns(t *testing.T) {
	ctx := context.Background()
	db, _, r := newPostgresRepo(t)
	seedPostgresUser(t, db, "Alice", true)

	rows, err := r.SelectColumns(ctx, "id, name, enabled")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Alice", rows[0].Name)
}

// TestPostgres_FindAllValues exercises the ([]T, error) return shape — a slice of values,
// as opposed to FindAll's []*User — to confirm both ReturnSlice variants work against a
// real database, not just that they compile.
func TestPostgres_FindAllValues(t *testing.T) {
	ctx := context.Background()
	db, _, r := newPostgresRepo(t)
	seedPostgresUser(t, db, "Alice", true)
	seedPostgresUser(t, db, "Bob", false)

	rows, err := r.FindAllValues(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "Alice", rows[0].Name)
	require.Equal(t, "Bob", rows[1].Name)
}

func TestPostgres_Create(t *testing.T) {
	ctx := context.Background()
	_, _, r := newPostgresRepo(t)

	id, err := r.Create(ctx, "Dave", true)
	require.NoError(t, err)
	require.Positive(t, id)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Dave", got.Name)
	require.True(t, got.Enabled)
}

func TestPostgres_UpdateEnabled(t *testing.T) {
	ctx := context.Background()
	db, _, r := newPostgresRepo(t)
	id := seedPostgresUser(t, db, "Alice", false)

	n, err := r.UpdateEnabled(ctx, id, true)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.True(t, got.Enabled)
}

func TestPostgres_DeleteById(t *testing.T) {
	ctx := context.Background()
	db, _, r := newPostgresRepo(t)
	id := seedPostgresUser(t, db, "Alice", true)

	n, err := r.DeleteById(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestPostgres_Rename(t *testing.T) {
	ctx := context.Background()
	db, _, r := newPostgresRepo(t)
	id := seedPostgresUser(t, db, "Alice", true)

	require.NoError(t, r.Rename(ctx, id, "Alicia"))

	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Alicia", got.Name)
}

func TestPostgres_FindByIdSync(t *testing.T) {
	db, _, r := newPostgresRepo(t)
	id := seedPostgresUser(t, db, "Alice", true)

	got, err := r.FindByIdSync(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "Alice", got.Name)
}

func TestPostgres_Transaction(t *testing.T) {
	ctx := context.Background()
	db, cm, r := newPostgresRepo(t)
	id := seedPostgresUser(t, db, "Alice", false)

	err := cm.StartTransaction(ctx, func(ctx context.Context) error {
		_, err := r.UpdateEnabled(ctx, id, true)
		return err
	})
	require.NoError(t, err)
	got, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.True(t, got.Enabled)

	boom := errors.New("boom")
	err = cm.StartTransaction(ctx, func(ctx context.Context) error {
		_, err := r.UpdateEnabled(ctx, id, false)
		if err != nil {
			return err
		}
		return boom
	})
	require.ErrorIs(t, err, boom)

	stillEnabled, err := r.FindById(ctx, id)
	require.NoError(t, err)
	require.True(t, stillEnabled.Enabled)
}
