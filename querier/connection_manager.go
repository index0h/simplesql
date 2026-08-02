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
// transaction, then commits on success or rolls back on error. If cb panics, the
// transaction is rolled back before the panic is repropagated, so a panicking
// callback never leaves the transaction open.
// If ctx already carries a transaction, cb is called directly without starting
// a new one, making nested calls safe; the panic/rollback handling above only
// applies at the outermost call, which is the one that actually owns tx.
func (cm *ConnectionManager) StartTransaction(ctx context.Context, cb func(ctx context.Context) error) error {
	_, ok := ctx.Value(txContextKey{}).(*sql.Tx)
	if ok {
		return cb(ctx)
	}

	tx, err := cm.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "begin transaction")
	}

	err = func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				// Best-effort cleanup: intentionally not joined into the repanic value,
				// since that would replace the original panic value/type with an error,
				// breaking recover()-based callers (e.g. testify's PanicsWithValue) that
				// expect it verbatim.
				_ = tx.Rollback()
				panic(r)
			}
		}()
		return cb(context.WithValue(ctx, txContextKey{}, tx))
	}()
	if err != nil {
		// errors.Join (not CombineErrors) matters here: CombineErrors attaches the
		// second error as a "secondary" that's invisible to both Error() and
		// errors.Is/As — Join gives a real multi-error via Unwrap() []error, so a
		// rollback failure alongside the callback error is actually observable.
		return errors.Join(err, tx.Rollback())
	}

	return errors.WithStack(tx.Commit())
}
