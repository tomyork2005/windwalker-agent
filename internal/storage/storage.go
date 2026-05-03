package storage

import (
	"context"
	"fmt"
	"time"

	"agent/internal/model"

	"github.com/georgysavva/scany/v2/sqlscan"
)

type taskRow struct {
	RequestID  string `db:"request_id"`
	UserID     string `db:"user_id"`
	DriverType string `db:"driver_type"`
	Kind       string `db:"kind"`
	ReceivedAt int64  `db:"received_at"`
}

func (r taskRow) toModel() *model.Task {
	return &model.Task{
		RequestID:  r.RequestID,
		UserID:     r.UserID,
		DriverType: r.DriverType,
		Kind:       model.TaskKind(r.Kind),
		ReceivedAt: time.Unix(r.ReceivedAt, 0),
	}
}

func (s *Storage) SaveTask(ctx context.Context, t *model.Task) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks(request_id, user_id, driver_type, kind, received_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(request_id) DO NOTHING
	`, t.RequestID, t.UserID, t.DriverType, string(t.Kind), t.ReceivedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}

	return nil
}

func (s *Storage) PullPendingTasks(ctx context.Context, limit int) ([]*model.Task, error) {
	var rows []taskRow

	err := sqlscan.Select(ctx, s.db, &rows, `
		SELECT request_id, user_id, driver_type, kind, received_at
		FROM tasks
		WHERE done_at IS NULL AND failed_at IS NULL
		ORDER BY received_at
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("select pending: %w", err)
	}

	out := make([]*model.Task, len(rows))
	for i := range rows {
		out[i] = rows[i].toModel()
	}

	return out, nil
}

func (s *Storage) MarkDone(ctx context.Context, requestID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET done_at = ?
		WHERE request_id = ? AND done_at IS NULL AND failed_at IS NULL
	`, time.Now().Unix(), requestID)
	if err != nil {
		return fmt.Errorf("mark done: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		s.log.Warn("mark done: task missing or already terminal", "request_id", requestID)
	}

	return nil
}

func (s *Storage) MarkFailed(ctx context.Context, requestID string, errMsg string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks SET failed_at = ?, last_error = ?
		WHERE request_id = ? AND done_at IS NULL AND failed_at IS NULL
	`, time.Now().Unix(), errMsg, requestID)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		s.log.Warn("mark failed: task missing or already terminal", "request_id", requestID)
	}

	return nil
}

func (s *Storage) ListUserIDs(ctx context.Context) ([]string, error) {
	var ids []string

	if err := sqlscan.Select(ctx, s.db, &ids,
		`SELECT DISTINCT user_id FROM users ORDER BY user_id`); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}

	return ids, nil
}

func (s *Storage) UpsertUser(ctx context.Context, userID, driverType string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users(user_id, driver_type, created_at, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(user_id, driver_type) DO UPDATE SET updated_at = excluded.updated_at
	`, userID, driverType, now, now)
	if err != nil {
		return fmt.Errorf("upsert user: %w", err)
	}

	return nil
}

func (s *Storage) DeleteUser(ctx context.Context, userID, driverType string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM users WHERE user_id = ? AND driver_type = ?`,
		userID, driverType)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}

	return nil
}
