package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"whisperserver/src/internal/domain"
)

// ErrFencingTokenMismatch is returned when a task update fails due to mismatched attempt or lease owner.
var ErrFencingTokenMismatch = errors.New("fencing token mismatch: task lease owner or attempt changed")

// ErrTaskNotLeased is returned when trying to update a task that is not leased.
var ErrTaskNotLeased = errors.New("task is not currently leased by this worker")

// sqlTxWrapper adapts *sql.Tx to domain.Tx.
type sqlTxWrapper struct {
	*sql.Tx
}

func (w *sqlTxWrapper) Commit() error {
	return w.Tx.Commit()
}

func (w *sqlTxWrapper) Rollback() error {
	return w.Tx.Rollback()
}

// TxManager handles beginning SQLite transactions implementing domain.TxManager.
type TxManager struct {
	db *sql.DB
}

// NewTxManager returns a new TxManager for the provided DB (or default shared dbConn).
func NewTxManager(db *sql.DB) *TxManager {
	if db == nil {
		db = dbConn
	}
	return &TxManager{db: db}
}

// BeginTx starts a new transaction satisfying domain.Tx.
func (m *TxManager) BeginTx(ctx context.Context) (domain.Tx, error) {
	targetDB := m.db
	if targetDB == nil {
		targetDB = dbConn
	}
	if targetDB == nil {
		return nil, errors.New("database connection is not initialized")
	}
	tx, err := targetDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &sqlTxWrapper{Tx: tx}, nil
}

// GetRawDBForTest returns the underlying *sql.DB for test assertion verification.
func GetRawDBForTest() *sql.DB {
	return dbConn
}

// GetRawTx safely unwraps domain.Tx into *sql.Tx.
func GetRawTx(tx domain.Tx) *sql.Tx {
	if tx == nil {
		return nil
	}
	if w, ok := tx.(*sqlTxWrapper); ok {
		return w.Tx
	}
	if stx, ok := tx.(*sql.Tx); ok {
		return stx
	}
	return nil
}

// extractTx aliases GetRawTx internally.
func extractTx(tx domain.Tx) *sql.Tx {
	return GetRawTx(tx)
}

type queryExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getExecutor(tx domain.Tx) queryExecutor {
	if unwrapped := extractTx(tx); unwrapped != nil {
		return unwrapped
	}
	return dbConn
}

