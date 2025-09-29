package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type Storage struct {
	db *sql.DB
}

// tx or db, to avoid duplicating code

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Storage) ex(ctx context.Context) execer {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return s.db
}

func (s *Storage) GetLastAppliedSeq(ctx context.Context) (uint64, error) {
	var val string
	err := s.ex(ctx).QueryRowContext(ctx,
		`SELECT value FROM meta WHERE key='last_applied_seq'`).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var seq uint64
	_, err = fmt.Sscan(val, &seq)
	return seq, err
}

func (s *Storage) SetLastAppliedSeq(ctx context.Context, seq uint64) error {
	_, err := s.ex(ctx).ExecContext(ctx, `
		INSERT INTO meta(key,value) VALUES('last_applied_seq', ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value
	`, fmt.Sprint(seq))
	return err
}
