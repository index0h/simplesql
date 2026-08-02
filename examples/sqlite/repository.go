package repo

import "context"

type User struct {
	ID      int    `db:"id"`
	Name    string `db:"name"`
	Enabled bool   `db:"enabled"`
}

type Balance struct {
	UserID int `db:"user_id"`
	Amount int `db:"amount"`
}

// UserBalance is a join result: the struct embeds two entities, and each is scanned
// from its own set of columns via querier.FieldPointers' embedded-field flattening.
type UserBalance struct {
	User
	Balance
}

type UserRepository interface {
	// FindById looks up a user by primary key, joined with their balance row.
	// Demonstrates: bound param reused in a {{if}} guard evaluated on the Go value.
	// @sql SELECT u.id, u.name, u.enabled FROM users u
	// LEFT JOIN balances b ON b.user_id = u.id
	// WHERE u.id = :id
	// {{if id > 1000}} AND u.id > 1000{{end}}
	FindById(ctx context.Context, id int) (*User, error)

	// FindAll lists users filtered by optional enabled flag and minimum id.
	// Demonstrates: nested if / else-if with optional pointer params.
	// @sql SELECT * FROM users WHERE 1=1
	// {{if enabled != nil}} AND enabled = :enabled{{if minId != nil}} AND id > :minId{{end}}{{else if minId != nil}} AND id > :minId{{end}}
	FindAll(ctx context.Context, enabled *bool, minId *int) ([]*User, error)

	// SelectColumns demonstrates direct string substitution ({{cols}}) for a dynamic column list.
	// @sql SELECT {{cols}} FROM users ORDER BY id
	SelectColumns(ctx context.Context, cols string) ([]*User, error)

	// Create inserts a user and returns the last insert id.
	// @sql INSERT INTO users (name, enabled) VALUES (:name, :enabled)
	Create(ctx context.Context, name string, enabled bool) (int, error)

	// UpdateEnabled flips the enabled flag and returns the number of affected rows.
	// @sql UPDATE users SET enabled = :enabled WHERE id = :id
	UpdateEnabled(ctx context.Context, id int, enabled bool) (int, error)

	// DeleteById removes a user and returns the number of affected rows.
	// @sql DELETE FROM users WHERE id = :id
	DeleteById(ctx context.Context, id int) (int, error)

	// Rename demonstrates the error-only return kind.
	// @sql UPDATE users SET name = :name WHERE id = :id
	Rename(ctx context.Context, id int, name string) error

	// FindByIdSync demonstrates the no-context path (context.Background() is used internally).
	// @sql SELECT * FROM users WHERE id = :id
	FindByIdSync(id int) (*User, error)

	// FindWithBalance joins users and balances into a single result struct that embeds
	// both entities. Demonstrates single-row scanning (querier.FieldPointers) into a
	// struct{ User; Balance }.
	// @sql SELECT u.id, u.name, u.enabled, b.user_id, b.amount FROM users u
	// INNER JOIN balances b ON b.user_id = u.id
	// WHERE u.id = :id
	FindWithBalance(ctx context.Context, id int) (*UserBalance, error)

	// ListWithBalances joins users and balances into a slice of the embedded result
	// struct. Demonstrates multi-row scanning (querier.ScanRows) into a struct{ User; Balance }.
	// @sql SELECT u.id, u.name, u.enabled, b.user_id, b.amount FROM users u
	// INNER JOIN balances b ON b.user_id = u.id
	// ORDER BY u.id
	ListWithBalances(ctx context.Context) ([]*UserBalance, error)
}
