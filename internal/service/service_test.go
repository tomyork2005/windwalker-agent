package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agent/internal/domain"
)

type mockStorage struct {
	lastSeq   uint64
	getErr    error
	setErr    error
	upsertErr error
	removeErr error

	getCalls    int
	setCalls    int
	upsertCalls int
	removeCalls int

	setSeq       uint64
	upsertUser   domain.User
	removeUserID string
}

func (m *mockStorage) GetLastAppliedSeq(_ context.Context) (uint64, error) {
	m.getCalls++
	return m.lastSeq, m.getErr
}

func (m *mockStorage) SetLastAppliedSeq(_ context.Context, seq uint64) error {
	m.setCalls++
	m.setSeq = seq
	return m.setErr
}

func (m *mockStorage) UpsertUser(_ context.Context, u domain.User) error {
	m.upsertCalls++
	m.upsertUser = u
	return m.upsertErr
}

func (m *mockStorage) RemoveUser(_ context.Context, userID string) error {
	m.removeCalls++
	m.removeUserID = userID
	return m.removeErr
}

type mockTxManager struct {
	calls int
	err   error
}

func (m *mockTxManager) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	m.calls++
	if m.err != nil {
		return m.err
	}
	return fn(ctx)
}

type mockDriverMultiplexer struct {
	upsertErr error
	removeErr error

	upsertCalls int
	removeCalls int

	upsertUser       domain.User
	removeUserID     string
	removeDriverType string
}

func (m *mockDriverMultiplexer) Upsert(ctx context.Context, user domain.User) error {
	m.upsertCalls++
	m.upsertUser = user
	return m.upsertErr
}

func (m *mockDriverMultiplexer) Remove(ctx context.Context, userID string, driverType string) error {
	m.removeCalls++
	m.removeUserID = userID
	m.removeDriverType = driverType
	return m.removeErr
}

func TestService_UpsertUser(t *testing.T) {
	tests := []struct {
		name    string
		meta    *domain.Meta
		user    *domain.User
		storage *mockStorage
		driver  *mockDriverMultiplexer
		tx      *mockTxManager
		wantErr string
		assert  func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager)
	}{
		{
			name:    "invalid payload",
			meta:    nil,
			user:    &domain.User{ID: "user-1"},
			storage: &mockStorage{},
			driver:  &mockDriverMultiplexer{},
			tx:      &mockTxManager{},
			wantErr: "invalid upsert payload",
			assert: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				if tx.calls != 0 {
					t.Fatalf("expected WithTx not to be called")
				}
				if driver.upsertCalls != 0 {
					t.Fatalf("expected driver upsert not to be called")
				}
				if storage.upsertCalls != 0 {
					t.Fatalf("expected storage upsert not to be called")
				}
			},
		},
		{
			name: "duplicate seq",
			meta: &domain.Meta{Seq: 5},
			user: &domain.User{ID: "user-1"},
			storage: &mockStorage{
				lastSeq: 6,
			},
			driver: &mockDriverMultiplexer{},
			tx:     &mockTxManager{},
			assert: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				if tx.calls != 1 {
					t.Fatalf("expected WithTx to be called once, got %d", tx.calls)
				}
				if storage.getCalls != 1 {
					t.Fatalf("expected storage get last seq to be called once, got %d", storage.getCalls)
				}
				if driver.upsertCalls != 0 {
					t.Fatalf("expected driver upsert not to be called")
				}
				if storage.upsertCalls != 0 {
					t.Fatalf("expected storage upsert not to be called")
				}
				if storage.setCalls != 0 {
					t.Fatalf("expected storage set seq not to be called")
				}
			},
		},
		{
			name:    "driver error",
			meta:    &domain.Meta{Seq: 10},
			user:    &domain.User{ID: "user-1"},
			storage: &mockStorage{lastSeq: 5},
			driver:  &mockDriverMultiplexer{upsertErr: errors.New("boom")},
			tx:      &mockTxManager{},
			wantErr: "driver upsert",
			assert: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				if driver.upsertCalls != 1 {
					t.Fatalf("expected driver upsert to be called once")
				}
				if storage.upsertCalls != 0 {
					t.Fatalf("expected storage upsert not to be called")
				}
				if storage.setCalls != 0 {
					t.Fatalf("expected storage set seq not to be called")
				}
			},
		},
		{
			name:    "storage error",
			meta:    &domain.Meta{Seq: 10},
			user:    &domain.User{ID: "user-1"},
			storage: &mockStorage{lastSeq: 5, upsertErr: errors.New("db failed")},
			driver:  &mockDriverMultiplexer{},
			tx:      &mockTxManager{},
			wantErr: "db upsert",
			assert: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				if driver.upsertCalls != 1 {
					t.Fatalf("expected driver upsert to be called once")
				}
				if storage.upsertCalls != 1 {
					t.Fatalf("expected storage upsert to be called once")
				}
				if storage.setCalls != 0 {
					t.Fatalf("expected storage set seq not to be called")
				}
			},
		},
		{
			name:    "success",
			meta:    &domain.Meta{Seq: 10},
			user:    &domain.User{ID: "user-1", Name: "Alice"},
			storage: &mockStorage{lastSeq: 5},
			driver:  &mockDriverMultiplexer{},
			tx:      &mockTxManager{},
			assert: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				if driver.upsertCalls != 1 {
					t.Fatalf("expected driver upsert to be called once")
				}
				if driver.upsertUser.ID != "user-1" {
					t.Fatalf("unexpected user passed to driver: %+v", driver.upsertUser)
				}
				if storage.upsertCalls != 1 {
					t.Fatalf("expected storage upsert to be called once")
				}
				if storage.upsertUser.ID != "user-1" {
					t.Fatalf("unexpected user passed to storage: %+v", storage.upsertUser)
				}
				if storage.setCalls != 1 {
					t.Fatalf("expected storage set seq to be called once")
				}
				if storage.setSeq != 10 {
					t.Fatalf("expected last seq to be set to 10, got %d", storage.setSeq)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := NewAgentService(tc.storage, tc.tx, tc.driver)

			err := service.UpsertUser(context.Background(), tc.meta, tc.user)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.assert(t, tc.storage, tc.driver, tc.tx)
		})
	}
}

func TestService_RemoveUser(t *testing.T) {
	tests := []struct {
		name       string
		meta       *domain.Meta
		userID     string
		driverType string
		storage    *mockStorage
		driver     *mockDriverMultiplexer
		tx         *mockTxManager
		wantErr    string
		assert     func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager)
	}{
		{
			name:       "invalid payload",
			meta:       nil,
			userID:     "user-1",
			driverType: "wireguard",
			storage:    &mockStorage{},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "invalid remove payload",
			assert: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				if tx.calls != 0 {
					t.Fatalf("expected WithTx not to be called")
				}
				if driver.removeCalls != 0 {
					t.Fatalf("expected driver remove not to be called")
				}
				if storage.removeCalls != 0 {
					t.Fatalf("expected storage remove not to be called")
				}
			},
		},
		{
			name:       "duplicate seq",
			meta:       &domain.Meta{Seq: 5},
			userID:     "user-1",
			driverType: "wireguard",
			storage:    &mockStorage{lastSeq: 7},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			assert: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				if tx.calls != 1 {
					t.Fatalf("expected WithTx to be called once, got %d", tx.calls)
				}
				if storage.getCalls != 1 {
					t.Fatalf("expected storage get last seq to be called once, got %d", storage.getCalls)
				}
				if driver.removeCalls != 0 {
					t.Fatalf("expected driver remove not to be called")
				}
				if storage.removeCalls != 0 {
					t.Fatalf("expected storage remove not to be called")
				}
				if storage.setCalls != 0 {
					t.Fatalf("expected storage set seq not to be called")
				}
			},
		},
		{
			name:       "driver error",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "wireguard",
			storage:    &mockStorage{lastSeq: 4},
			driver:     &mockDriverMultiplexer{removeErr: errors.New("boom")},
			tx:         &mockTxManager{},
			wantErr:    "driver remove",
			assert: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				if driver.removeCalls != 1 {
					t.Fatalf("expected driver remove to be called once")
				}
				if storage.removeCalls != 0 {
					t.Fatalf("expected storage remove not to be called")
				}
				if storage.setCalls != 0 {
					t.Fatalf("expected storage set seq not to be called")
				}
			},
		},
		{
			name:       "storage error",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "wireguard",
			storage:    &mockStorage{lastSeq: 4, removeErr: errors.New("db failed")},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "db remove",
			assert: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				if driver.removeCalls != 1 {
					t.Fatalf("expected driver remove to be called once")
				}
				if storage.removeCalls != 1 {
					t.Fatalf("expected storage remove to be called once")
				}
				if storage.setCalls != 0 {
					t.Fatalf("expected storage set seq not to be called")
				}
			},
		},
		{
			name:       "success",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "wireguard",
			storage:    &mockStorage{lastSeq: 4},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			assert: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				if driver.removeCalls != 1 {
					t.Fatalf("expected driver remove to be called once")
				}
				if driver.removeUserID != "user-1" || driver.removeDriverType != "wireguard" {
					t.Fatalf("unexpected args passed to driver: id=%s type=%s", driver.removeUserID, driver.removeDriverType)
				}
				if storage.removeCalls != 1 {
					t.Fatalf("expected storage remove to be called once")
				}
				if storage.removeUserID != "user-1" {
					t.Fatalf("unexpected user id passed to storage remove: %s", storage.removeUserID)
				}
				if storage.setCalls != 1 {
					t.Fatalf("expected storage set seq to be called once")
				}
				if storage.setSeq != 10 {
					t.Fatalf("expected last seq to be set to 10, got %d", storage.setSeq)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := NewAgentService(tc.storage, tc.tx, tc.driver)

			err := service.RemoveUser(context.Background(), tc.meta, tc.userID, tc.driverType)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.assert(t, tc.storage, tc.driver, tc.tx)
		})
	}
}
