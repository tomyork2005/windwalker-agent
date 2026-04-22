package storage

import (
	"agent/internal/domain"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Storage struct {
	db        *sql.DB
	txManager *TxManager
}

// interface and func ex --> to avoid duplicating code

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

func (s *Storage) GetTxManager() *TxManager {
	return s.txManager
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

func (s *Storage) UpsertUser(ctx context.Context, u domain.User) error {
	var exp any
	if !u.ExpiresAt.IsZero() {
		exp = u.ExpiresAt.Unix()
	}

	_, err := s.ex(ctx).ExecContext(ctx, `
		INSERT INTO users(id,account_id,driver_type,expires_at,updated_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  account_id=excluded.account_id,
		  driver_type=excluded.driver_type,
		  expires_at=excluded.expires_at,
		  updated_at=excluded.updated_at
	`, u.ID, u.AccountID, u.DriverType, exp, time.Now().Unix())
	return err
}

func (s *Storage) RemoveUser(ctx context.Context, userID string) error {
	_, err := s.ex(ctx).ExecContext(ctx, `DELETE FROM users WHERE id=?`, userID)
	return err
}

func (s *Storage) Close() error {
	return s.db.Close()
}
