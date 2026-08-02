package querier

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

func newMockConnectionManager(t *testing.T) (*ConnectionManager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewConnectionManager(db), mock
}

func TestConnectionManager_DB_NoTransactionReturnsUnderlyingDB(t *testing.T) {
	cm, mock := newMockConnectionManager(t)

	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, err := cm.DB(context.Background()).QueryContext(context.Background(), "SELECT 1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectionManager_StartTransaction_CommitsOnSuccess(t *testing.T) {
	cm, mock := newMockConnectionManager(t)

	mock.ExpectBegin()
	mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := cm.StartTransaction(context.Background(), func(ctx context.Context) error {
		_, err := cm.DB(ctx).ExecContext(ctx, "INSERT INTO t VALUES (1)")
		return err
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectionManager_StartTransaction_RollsBackOnCallbackError(t *testing.T) {
	cm, mock := newMockConnectionManager(t)

	mock.ExpectBegin()
	mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectRollback()

	boom := errors.New("boom")
	err := cm.StartTransaction(context.Background(), func(ctx context.Context) error {
		_, execErr := cm.DB(ctx).ExecContext(ctx, "INSERT INTO t VALUES (1)")
		if execErr != nil {
			return execErr
		}
		return boom
	})
	require.ErrorIs(t, err, boom)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestConnectionManager_StartTransaction_JoinsRollbackErrorWithCallbackError covers the
// case that actually distinguishes errors.Join from errors.CombineErrors: an existing
// (callback) error already set, with a rollback error on top. CombineErrors would have
// silently dropped the rollback error here (it attaches it as a "secondary" that
// Error()/errors.Is never surface); Join must not.
func TestConnectionManager_StartTransaction_JoinsRollbackErrorWithCallbackError(t *testing.T) {
	cm, mock := newMockConnectionManager(t)

	mock.ExpectBegin()
	rollbackErr := errors.New("rollback failed")
	mock.ExpectRollback().WillReturnError(rollbackErr)

	boom := errors.New("boom")
	err := cm.StartTransaction(context.Background(), func(ctx context.Context) error {
		return boom
	})
	require.ErrorIs(t, err, boom, "callback error must be discoverable")
	require.ErrorIs(t, err, rollbackErr, "rollback error must be discoverable even alongside the callback error")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestConnectionManager_StartTransaction_RollsBackOnPanic verifies that a panicking
// callback still rolls back the transaction (instead of leaving it open) and that the
// panic itself is repropagated to the caller rather than swallowed.
func TestConnectionManager_StartTransaction_RollsBackOnPanic(t *testing.T) {
	cm, mock := newMockConnectionManager(t)

	mock.ExpectBegin()
	mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectRollback()

	boom := "boom"
	require.PanicsWithValue(t, boom, func() {
		_ = cm.StartTransaction(context.Background(), func(ctx context.Context) error {
			_, err := cm.DB(ctx).ExecContext(ctx, "INSERT INTO t VALUES (1)")
			require.NoError(t, err)
			panic(boom)
		})
	})
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestConnectionManager_StartTransaction_NestedPanicRollsBackOuterTx verifies that a
// panic from a nested StartTransaction call still rolls back the one real transaction
// owned by the outermost call.
func TestConnectionManager_StartTransaction_NestedPanicRollsBackOuterTx(t *testing.T) {
	cm, mock := newMockConnectionManager(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	boom := "boom"
	require.PanicsWithValue(t, boom, func() {
		_ = cm.StartTransaction(context.Background(), func(ctx context.Context) error {
			return cm.StartTransaction(ctx, func(ctx context.Context) error {
				panic(boom)
			})
		})
	})
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectionManager_StartTransaction_RollsBackOnBeginError(t *testing.T) {
	cm, mock := newMockConnectionManager(t)

	beginErr := errors.New("connection refused")
	mock.ExpectBegin().WillReturnError(beginErr)

	called := false
	err := cm.StartTransaction(context.Background(), func(ctx context.Context) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, beginErr)
	require.False(t, called, "callback must not run if BeginTx fails")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestConnectionManager_StartTransaction_NestedReusesOuterTx verifies that a nested
// StartTransaction call (ctx already carries a *sql.Tx) does not begin a second
// transaction, and runs its callback directly under the outer one. Only a single
// ExpectBegin/ExpectCommit pair is registered; if the nested call tried to begin its
// own transaction, sqlmock would fail this test with an unexpected call to Begin.
func TestConnectionManager_StartTransaction_NestedReusesOuterTx(t *testing.T) {
	cm, mock := newMockConnectionManager(t)

	mock.ExpectBegin()
	mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	innerRan := false
	err := cm.StartTransaction(context.Background(), func(ctx context.Context) error {
		_, err := cm.DB(ctx).ExecContext(ctx, "INSERT INTO t VALUES (1)")
		if err != nil {
			return err
		}
		return cm.StartTransaction(ctx, func(ctx context.Context) error {
			innerRan = true
			_, err := cm.DB(ctx).ExecContext(ctx, "INSERT INTO t VALUES (2)")
			return err
		})
	})
	require.NoError(t, err)
	require.True(t, innerRan)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectionManager_DB_ReturnsTxFromContext(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectCommit()

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)

	cm := NewConnectionManager(db)
	ctx := context.WithValue(context.Background(), txContextKey{}, tx)

	got := cm.DB(ctx)
	q, ok := got.(*sql.Tx)
	require.True(t, ok)
	require.Same(t, tx, q)

	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}
