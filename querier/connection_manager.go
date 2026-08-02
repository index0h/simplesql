package querier

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
)

type txContextKey struct{}

// ConnectionManager wraps a *sql.DB and provides transaction management.
type ConnectionManager struct {
	db *sql.DB
}

// NewConnectionManager returns a ConnectionManager for the given db.
func NewConnectionManager(db *sql.DB) *ConnectionManager {
	return &ConnectionManager{db: db}
}

// DB returns the active transaction from ctx if one exists, otherwise returns
// the underlying *sql.DB. Pass the result into generated repository constructors.
func (cm *ConnectionManager) DB(ctx context.Context) Querier {
	tx, ok := ctx.Value(txContextKey{}).(*sql.Tx)
	if ok {
		return tx
	}
	return cm.db
}

// StartTransaction begins a transaction, calls cb with a context carrying the
// transaction, then commits on success or rolls back on error.
// If ctx already carries a transaction, cb is called directly without starting
// a new one, making nested calls safe.
func (cm *ConnectionManager) StartTransaction(ctx context.Context, cb func(ctx context.Context) error) error {
	_, ok := ctx.Value(txContextKey{}).(*sql.Tx)
	if ok {
		return cb(ctx)
	}

	tx, err := cm.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "begin transaction")
	}

	err = cb(context.WithValue(ctx, txContextKey{}, tx))
	if err != nil {
		return errors.CombineErrors(err, tx.Rollback())
	}

	return errors.WithStack(tx.Commit())
}
