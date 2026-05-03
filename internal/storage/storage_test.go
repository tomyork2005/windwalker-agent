package storage_test

import (
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent/internal/config"
	"agent/internal/model"
	"agent/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func newTestStorage(t *testing.T) (*storage.Storage, string) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	applyInitMigration(t, dbPath)

	cfg := config.SQLiteConfig{
		Path:        dbPath,
		BusyTimeout: 5 * time.Second,
		Wal:         true,
		ForeignKeys: true,
	}
	s, err := storage.New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s, dbPath
}

func applyInitMigration(t *testing.T, dbPath string) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "00001_init.sql"))
	require.NoError(t, err)

	upSQL := extractGooseUp(t, string(raw))

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.ExecContext(t.Context(), upSQL)
	require.NoError(t, err, "apply migration")
}

// extractGooseUp returns the SQL between `-- +goose Up` and `-- +goose Down`.
func extractGooseUp(t *testing.T, src string) string {
	t.Helper()
	upIdx := strings.Index(src, "-- +goose Up")
	require.GreaterOrEqual(t, upIdx, 0, "no -- +goose Up marker")
	rest := src[upIdx+len("-- +goose Up"):]
	if down := strings.Index(rest, "-- +goose Down"); down >= 0 {
		rest = rest[:down]
	}
	return rest
}

func TestStorage_EndToEnd(t *testing.T) {
	t.Parallel()

	s, dbPath := newTestStorage(t)
	ctx := t.Context()

	now := time.Now().Truncate(time.Second)

	t.Run("SaveTask is idempotent on duplicate request_id", func(t *testing.T) {
		task := &model.Task{
			RequestID:  "rid-dup",
			UserID:     "u1",
			DriverType: "xray",
			Kind:       model.TaskUpsert,
			ReceivedAt: now,
		}
		require.NoError(t, s.SaveTask(ctx, task))
		require.NoError(t, s.SaveTask(ctx, task))

		pending, err := s.PullPendingTasks(ctx, 10)
		require.NoError(t, err)
		var found int
		for _, p := range pending {
			if p.RequestID == "rid-dup" {
				found++
			}
		}
		assert.Equal(t, 1, found)

		require.NoError(t, s.MarkDone(ctx, "rid-dup"))
	})

	t.Run("PullPendingTasks orders by received_at ASC", func(t *testing.T) {
		base := now.Add(time.Hour)
		require.NoError(t, s.SaveTask(ctx, &model.Task{
			RequestID: "rid-order-3", UserID: "u", DriverType: "xray",
			Kind: model.TaskUpsert, ReceivedAt: base.Add(2 * time.Second),
		}))
		require.NoError(t, s.SaveTask(ctx, &model.Task{
			RequestID: "rid-order-1", UserID: "u", DriverType: "xray",
			Kind: model.TaskUpsert, ReceivedAt: base,
		}))
		require.NoError(t, s.SaveTask(ctx, &model.Task{
			RequestID: "rid-order-2", UserID: "u", DriverType: "xray",
			Kind: model.TaskUpsert, ReceivedAt: base.Add(1 * time.Second),
		}))

		pending, err := s.PullPendingTasks(ctx, 100)
		require.NoError(t, err)

		var got []string
		for _, p := range pending {
			if strings.HasPrefix(p.RequestID, "rid-order-") {
				got = append(got, p.RequestID)
			}
		}
		assert.Equal(t, []string{"rid-order-1", "rid-order-2", "rid-order-3"}, got)

		for _, rid := range got {
			require.NoError(t, s.MarkDone(ctx, rid))
		}
	})

	t.Run("MarkDone removes task from pending", func(t *testing.T) {
		require.NoError(t, s.SaveTask(ctx, &model.Task{
			RequestID: "rid-done", UserID: "u", DriverType: "xray",
			Kind: model.TaskUpsert, ReceivedAt: now,
		}))
		require.NoError(t, s.MarkDone(ctx, "rid-done"))

		pending, err := s.PullPendingTasks(ctx, 10)
		require.NoError(t, err)
		for _, p := range pending {
			assert.NotEqual(t, "rid-done", p.RequestID)
		}
	})

	t.Run("MarkDone is idempotent on missing request_id", func(t *testing.T) {
		assert.NoError(t, s.MarkDone(ctx, "does-not-exist"))
	})

	t.Run("MarkFailed marks task terminal", func(t *testing.T) {
		require.NoError(t, s.SaveTask(ctx, &model.Task{
			RequestID: "rid-fail", UserID: "u", DriverType: "xray",
			Kind: model.TaskUpsert, ReceivedAt: now,
		}))
		require.NoError(t, s.MarkFailed(ctx, "rid-fail", "boom"))

		pending, err := s.PullPendingTasks(ctx, 10)
		require.NoError(t, err)
		for _, p := range pending {
			assert.NotEqual(t, "rid-fail", p.RequestID)
		}
	})

	t.Run("MarkFailed is no-op on already-done task", func(t *testing.T) {
		raw, err := sql.Open("sqlite", dbPath)
		require.NoError(t, err)
		defer raw.Close()

		require.NoError(t, s.SaveTask(ctx, &model.Task{
			RequestID: "rid-done-then-fail", UserID: "u", DriverType: "xray",
			Kind: model.TaskUpsert, ReceivedAt: now,
		}))
		require.NoError(t, s.MarkDone(ctx, "rid-done-then-fail"))
		require.NoError(t, s.MarkFailed(ctx, "rid-done-then-fail", "should not stick"))

		var failedAt sql.NullInt64
		err = raw.QueryRowContext(ctx,
			`SELECT failed_at FROM tasks WHERE request_id = ?`,
			"rid-done-then-fail").Scan(&failedAt)
		require.NoError(t, err)
		assert.False(t, failedAt.Valid, "failed_at should remain NULL")
	})

	t.Run("UpsertUser + ListUserIDs returns DISTINCT user_ids", func(t *testing.T) {
		require.NoError(t, s.UpsertUser(ctx, "user-A", "xray"))
		require.NoError(t, s.UpsertUser(ctx, "user-B", "xray"))
		require.NoError(t, s.UpsertUser(ctx, "user-A", "shadowsocks"))

		ids, err := s.ListUserIDs(ctx)
		require.NoError(t, err)
		assert.Contains(t, ids, "user-A")
		assert.Contains(t, ids, "user-B")

		var countA int
		for _, id := range ids {
			if id == "user-A" {
				countA++
			}
		}
		assert.Equal(t, 1, countA)
	})

	t.Run("UpsertUser idempotent — updates updated_at, keeps created_at", func(t *testing.T) {
		raw, err := sql.Open("sqlite", dbPath)
		require.NoError(t, err)
		defer raw.Close()

		require.NoError(t, s.UpsertUser(ctx, "user-idem", "xray"))

		var createdAt1, updatedAt1 int64
		err = raw.QueryRowContext(ctx,
			`SELECT created_at, updated_at FROM users WHERE user_id = ? AND driver_type = ?`,
			"user-idem", "xray").Scan(&createdAt1, &updatedAt1)
		require.NoError(t, err)

		time.Sleep(1100 * time.Millisecond) // unix-second granularity in DB

		require.NoError(t, s.UpsertUser(ctx, "user-idem", "xray"))

		var createdAt2, updatedAt2 int64
		err = raw.QueryRowContext(ctx,
			`SELECT created_at, updated_at FROM users WHERE user_id = ? AND driver_type = ?`,
			"user-idem", "xray").Scan(&createdAt2, &updatedAt2)
		require.NoError(t, err)

		assert.Equal(t, createdAt1, createdAt2, "created_at must not change")
		assert.Greater(t, updatedAt2, updatedAt1, "updated_at must advance")
	})

	t.Run("DeleteUser removes one row, idempotent on missing", func(t *testing.T) {
		require.NoError(t, s.UpsertUser(ctx, "user-del", "xray"))

		idsBefore, err := s.ListUserIDs(ctx)
		require.NoError(t, err)
		assert.Contains(t, idsBefore, "user-del")

		require.NoError(t, s.DeleteUser(ctx, "user-del", "xray"))

		idsAfter, err := s.ListUserIDs(ctx)
		require.NoError(t, err)
		assert.NotContains(t, idsAfter, "user-del")

		assert.NoError(t, s.DeleteUser(ctx, "user-del", "xray"))
		assert.NoError(t, s.DeleteUser(ctx, "never-existed", "xray"))
	})
}
