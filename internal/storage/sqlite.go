package storage

import (
	"agent/internal/config"
	"context"
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
)

func NewSQLiteStorage(ctx context.Context, cfg config.SQLiteConfig) (*Storage, error) {
	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	pragma := []string{
		fmt.Sprintf("PRAGMA journal_mode=%s;", map[bool]string{true: "WAL", false: "DELETE"}[cfg.Wal]),
		fmt.Sprintf("PRAGMA synchronous=%s;", map[bool]string{true: "FULL", false: "NORMAL"}[cfg.SynchronousFull]),
		fmt.Sprintf("PRAGMA busy_timeout=%d;", int(cfg.BusyTimeout.Milliseconds())),
		fmt.Sprintf("PRAGMA foreign_keys=%d;", map[bool]int{true: 1, false: 0}[cfg.ForeignKeys]),

		"PRAGMA temp_store=MEMORY;",
		"PRAGMA cache_size=-20000;",
		"PRAGMA mmap_size=134217728;",
	}

	for _, q := range pragma {
		if _, err := db.ExecContext(ctx, q); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply PRAGMA %q: %w", q, err)
		}
	}

	if err := initSQLiteSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	txManager := NewTxManager(db)

	return &Storage{db: db, txManager: txManager}, nil
}

// initSQL + migration by PRAGMA user_version
func initSQLiteSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var ver int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version;`).Scan(&ver); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	switch ver {
	case 0:
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS meta (
				key   TEXT PRIMARY KEY,
				value ITEGER NOT NULL
			);
		`); err != nil {
			return fmt.Errorf("create meta: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS users (
				id          TEXT PRIMARY KEY,
				name        TEXT NOT NULL,
				driver_type TEXT NOT NULL,
				creds_json  TEXT NOT NULL,
				expires_at  INTEGER,
				updated_at  INTEGER NOT NULL 
			);
		`); err != nil {
			return fmt.Errorf("create users: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			CREATE INDEX IF NOT EXISTS idx_users_driver_type ON users(driver_type);
		`); err != nil {
			return fmt.Errorf("create idx_users_driver_type: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			CREATE INDEX IF NOT EXISTS idx_users_expired_at ON users(expires_at);
		`); err != nil {
			return fmt.Errorf("create idx_users_expired_at: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO meta(key, value) VALUES ('last_applied_seq', '0');
		`); err != nil {
			return fmt.Errorf("init meta.last_applied_seq: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `PRAGMA user_version=1;`); err != nil {
			return fmt.Errorf("set user_version: %w", err)
		}

	case 1:
		// new migration add here
	default:

	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema: %w", err)
	}
	return nil
}
