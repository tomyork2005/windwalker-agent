package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"agent/internal/config"

	_ "modernc.org/sqlite"
)

type Storage struct {
	db  *sql.DB
	log *slog.Logger
}

func New(ctx context.Context, cfg config.SQLiteConfig, log *slog.Logger) (*Storage, error) {
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "storage")

	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	pragmas := []string{
		fmt.Sprintf("PRAGMA journal_mode=%s;", journalMode(cfg.Wal)),
		fmt.Sprintf("PRAGMA synchronous=%s;", syncMode(cfg.SynchronousFull)),
		fmt.Sprintf("PRAGMA busy_timeout=%d;", cfg.BusyTimeout.Milliseconds()),
		fmt.Sprintf("PRAGMA foreign_keys=%s;", boolPragma(cfg.ForeignKeys)),
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA cache_size=-20000;",
		"PRAGMA mmap_size=134217728;",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply %q: %w", p, err)
		}
	}

	return &Storage{db: db, log: log}, nil
}

func (s *Storage) Close() error { return s.db.Close() }

func journalMode(wal bool) string {
	if wal {
		return "WAL"
	}
	return "DELETE"
}

func syncMode(full bool) string {
	if full {
		return "FULL"
	}
	return "NORMAL"
}

func boolPragma(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}
