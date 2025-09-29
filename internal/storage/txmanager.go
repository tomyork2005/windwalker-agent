package storage

import (
	"context"
	"database/sql"
)

const contextTxKey = "contextTx"

type TxManager struct {
	DB *sql.DB
}

func NewTxManager(db *sql.DB) *TxManager {
	return &TxManager{DB: db}
}

func (m *TxManager) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if tx := TxFromContext(ctx); tx != nil {
		return fn(ctx) // already in tx
	}
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	ctx = contextWithTx(ctx, tx)
	if err := fn(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func contextWithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, contextTxKey, tx)
}

func TxFromContext(ctx context.Context) *sql.Tx {
	if v := ctx.Value(contextTxKey); v != nil {
		if tx, ok := v.(*sql.Tx); ok {
			return tx
		}
	}
	return nil
}
