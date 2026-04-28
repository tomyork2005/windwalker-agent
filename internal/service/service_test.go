package service

import (
	"context"
	"errors"
	"testing"
	"time"

	controlpb "agent/api/control"
	"agent/internal/domain"
	"agent/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStorage struct {
	lastSeq   uint64
	getErr    error
	setErr    error
	upsertErr error
	removeErr error
	renewErr  error

	getCalls    int
	setCalls    int
	upsertCalls int
	removeCalls int
	renewCalls  int

	setSeq          uint64
	upsertUser      domain.User
	removeUserID    string
	renewUserID     string
	renewDriverType string
	renewExpiresAt  time.Time
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

func (m *mockStorage) RenewUser(_ context.Context, userID, driverType string, expiresAt time.Time) error {
	m.renewCalls++
	m.renewUserID = userID
	m.renewDriverType = driverType
	m.renewExpiresAt = expiresAt
	return m.renewErr
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
	upsertErr     error
	removeErr     error
	buildCredsErr error

	upsertCalls     int
	removeCalls     int
	buildCredsCalls int

	upsertUser       domain.User
	removeUserID     string
	removeDriverType string

	credsResp *controlpb.UserCreds
}

func (m *mockDriverMultiplexer) Upsert(_ context.Context, user domain.User) error {
	m.upsertCalls++
	m.upsertUser = user
	return m.upsertErr
}

func (m *mockDriverMultiplexer) Remove(_ context.Context, userID string, driverType string) error {
	m.removeCalls++
	m.removeUserID = userID
	m.removeDriverType = driverType
	return m.removeErr
}

func (m *mockDriverMultiplexer) BuildCreds(user domain.User) (*controlpb.UserCreds, error) {
	m.buildCredsCalls++
	if m.buildCredsErr != nil {
		return nil, m.buildCredsErr
	}
	if m.credsResp != nil {
		return m.credsResp, nil
	}
	return &controlpb.UserCreds{UserId: user.ID, DriverType: user.DriverType}, nil
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
		assertF func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager)
	}{
		{
			name:    "invalid payload",
			meta:    nil,
			user:    &domain.User{ID: "user-1"},
			storage: &mockStorage{},
			driver:  &mockDriverMultiplexer{},
			tx:      &mockTxManager{},
			wantErr: "invalid upsert request payload",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 0, tx.calls, "WithTx should not be called")
				assert.Equal(t, 0, driver.upsertCalls, "driver.Upsert should not be called")
				assert.Equal(t, 0, storage.upsertCalls, "storage.UpsertUser should not be called")
			},
		},
		{
			name:    "duplicate seq",
			meta:    &domain.Meta{Seq: 5},
			user:    &domain.User{ID: "user-1"},
			storage: &mockStorage{lastSeq: 6},
			driver:  &mockDriverMultiplexer{},
			tx:      &mockTxManager{},
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, tx.calls)
				assert.Equal(t, 1, storage.getCalls)
				assert.Equal(t, 0, driver.upsertCalls)
				assert.Equal(t, 0, storage.upsertCalls)
				assert.Equal(t, 0, storage.setCalls)
			},
		},
		{
			name:    "driver error",
			meta:    &domain.Meta{Seq: 10},
			user:    &domain.User{ID: "user-1"},
			storage: &mockStorage{lastSeq: 5},
			driver:  &mockDriverMultiplexer{upsertErr: errors.New("boom")},
			tx:      &mockTxManager{},
			wantErr: "driver upsert user",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, driver.upsertCalls)
				assert.Equal(t, 0, storage.upsertCalls)
				assert.Equal(t, 0, storage.setCalls)
			},
		},
		{
			name:    "storage user error",
			meta:    &domain.Meta{Seq: 10},
			user:    &domain.User{ID: "user-1"},
			storage: &mockStorage{lastSeq: 5, upsertErr: errors.New("db failed")},
			driver:  &mockDriverMultiplexer{},
			tx:      &mockTxManager{},
			wantErr: "storage upsert user",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, driver.upsertCalls)
				assert.Equal(t, 1, storage.upsertCalls)
				assert.Equal(t, 0, storage.setCalls)
			},
		},
		{
			name:    "storage seq error",
			meta:    &domain.Meta{Seq: 10},
			user:    &domain.User{ID: "user-1"},
			storage: &mockStorage{lastSeq: 5, setErr: errors.New("db failed")},
			driver:  &mockDriverMultiplexer{},
			tx:      &mockTxManager{},
			wantErr: "storage set last applied seq",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, driver.upsertCalls)
				assert.Equal(t, 1, storage.upsertCalls)
				assert.Equal(t, 1, storage.setCalls)
			},
		},
		{
			name:    "success",
			meta:    &domain.Meta{Seq: 10},
			user:    &domain.User{ID: "user-1", AccountID: "acc-1", DriverType: "xray"},
			storage: &mockStorage{lastSeq: 5},
			driver:  &mockDriverMultiplexer{},
			tx:      &mockTxManager{},
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, driver.upsertCalls)
				assert.Equal(t, "user-1", driver.upsertUser.ID)

				assert.Equal(t, 1, storage.upsertCalls)
				assert.Equal(t, "user-1", storage.upsertUser.ID)

				assert.Equal(t, 1, storage.setCalls)
				assert.Equal(t, uint64(10), storage.setSeq)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := NewAgentService(tc.storage, tc.tx, tc.driver)

			_, err := service.UpsertUser(context.Background(), tc.meta, tc.user)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}

			tc.assertF(t, tc.storage, tc.driver, tc.tx)
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
		assertF    func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager)
	}{
		{
			name:       "invalid payload",
			meta:       nil,
			userID:     "user-1",
			driverType: "xray",
			storage:    &mockStorage{},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "invalid remove request payload",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 0, tx.calls)
				assert.Equal(t, 0, driver.removeCalls)
				assert.Equal(t, 0, storage.removeCalls)
			},
		},
		{
			name:       "duplicate seq",
			meta:       &domain.Meta{Seq: 5},
			userID:     "user-1",
			driverType: "xray",
			storage:    &mockStorage{lastSeq: 7},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, tx.calls)
				assert.Equal(t, 1, storage.getCalls)
				assert.Equal(t, 0, driver.removeCalls)
				assert.Equal(t, 0, storage.removeCalls)
				assert.Equal(t, 0, storage.setCalls)
			},
		},
		{
			name:       "driver error",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "xray",
			storage:    &mockStorage{lastSeq: 4},
			driver:     &mockDriverMultiplexer{removeErr: errors.New("fail")},
			tx:         &mockTxManager{},
			wantErr:    "driver remove user",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, driver.removeCalls)
				assert.Equal(t, 0, storage.removeCalls)
				assert.Equal(t, 0, storage.setCalls)
			},
		},
		{
			name:       "storage user error",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "xray",
			storage:    &mockStorage{lastSeq: 4, removeErr: errors.New("db failed")},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "storage remove user",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, driver.removeCalls)
				assert.Equal(t, 1, storage.removeCalls)
				assert.Equal(t, 0, storage.setCalls)
			},
		},
		{
			name:       "storage seq error",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "xray",
			storage:    &mockStorage{lastSeq: 4, setErr: errors.New("db failed")},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "storage set last applied seq",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, driver.removeCalls)
				assert.Equal(t, 1, storage.removeCalls)
				assert.Equal(t, 1, storage.setCalls)
			},
		},
		{
			name:       "success",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "xray",
			storage:    &mockStorage{lastSeq: 4},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, driver.removeCalls)
				assert.Equal(t, "user-1", driver.removeUserID)
				assert.Equal(t, "xray", driver.removeDriverType)

				assert.Equal(t, 1, storage.removeCalls)
				assert.Equal(t, "user-1", storage.removeUserID)

				assert.Equal(t, 1, storage.setCalls)
				assert.Equal(t, uint64(10), storage.setSeq)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := NewAgentService(tc.storage, tc.tx, tc.driver)

			err := service.RemoveUser(context.Background(), tc.meta, tc.userID, tc.driverType)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}

			tc.assertF(t, tc.storage, tc.driver, tc.tx)
		})
	}
}

func TestService_RenewUser(t *testing.T) {
	exp := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		meta       *domain.Meta
		userID     string
		driverType string
		expiresAt  time.Time
		storage    *mockStorage
		driver     *mockDriverMultiplexer
		tx         *mockTxManager
		wantErr    string
		wantErrIs  error
		assertF    func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager)
	}{
		{
			name:       "invalid payload nil meta",
			meta:       nil,
			userID:     "user-1",
			driverType: "xray",
			expiresAt:  exp,
			storage:    &mockStorage{},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "invalid renew request payload",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 0, tx.calls)
				assert.Equal(t, 0, storage.renewCalls)
			},
		},
		{
			name:       "invalid payload empty user id",
			meta:       &domain.Meta{Seq: 10},
			userID:     "",
			driverType: "xray",
			expiresAt:  exp,
			storage:    &mockStorage{},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "invalid renew request payload",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 0, tx.calls)
				assert.Equal(t, 0, storage.renewCalls)
			},
		},
		{
			name:       "invalid payload empty driver type",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "",
			expiresAt:  exp,
			storage:    &mockStorage{},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "invalid renew request payload",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 0, tx.calls)
				assert.Equal(t, 0, storage.renewCalls)
			},
		},
		{
			name:       "duplicate seq",
			meta:       &domain.Meta{Seq: 5},
			userID:     "user-1",
			driverType: "xray",
			expiresAt:  exp,
			storage:    &mockStorage{lastSeq: 7},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, tx.calls)
				assert.Equal(t, 1, storage.getCalls)
				assert.Equal(t, 0, storage.renewCalls)
				assert.Equal(t, 0, storage.setCalls)
			},
		},
		{
			name:       "storage renew error",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "xray",
			expiresAt:  exp,
			storage:    &mockStorage{lastSeq: 4, renewErr: errors.New("db failed")},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "storage renew user",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, storage.renewCalls)
				assert.Equal(t, 0, storage.setCalls)
			},
		},
		{
			name:       "storage user not found",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "xray",
			expiresAt:  exp,
			storage:    &mockStorage{lastSeq: 4, renewErr: storage.ErrUserNotFound},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "user not found",
			wantErrIs:  storage.ErrUserNotFound,
			assertF: func(t *testing.T, s *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, s.renewCalls)
				assert.Equal(t, 0, s.setCalls)
			},
		},
		{
			name:       "storage seq error",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "xray",
			expiresAt:  exp,
			storage:    &mockStorage{lastSeq: 4, setErr: errors.New("db failed")},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			wantErr:    "storage set last applied seq",
			assertF: func(t *testing.T, storage *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, storage.renewCalls)
				assert.Equal(t, 1, storage.setCalls)
			},
		},
		{
			name:       "success",
			meta:       &domain.Meta{Seq: 10},
			userID:     "user-1",
			driverType: "xray",
			expiresAt:  exp,
			storage:    &mockStorage{lastSeq: 4},
			driver:     &mockDriverMultiplexer{},
			tx:         &mockTxManager{},
			assertF: func(t *testing.T, s *mockStorage, driver *mockDriverMultiplexer, tx *mockTxManager) {
				assert.Equal(t, 1, s.renewCalls)
				assert.Equal(t, "user-1", s.renewUserID)
				assert.Equal(t, "xray", s.renewDriverType)
				assert.True(t, s.renewExpiresAt.Equal(exp))
				assert.Equal(t, 1, s.setCalls)
				assert.Equal(t, uint64(10), s.setSeq)
				assert.Equal(t, 0, driver.removeCalls)
				assert.Equal(t, 0, driver.upsertCalls)
				assert.Equal(t, 0, driver.buildCredsCalls)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := NewAgentService(tc.storage, tc.tx, tc.driver)

			err := service.RenewUser(context.Background(), tc.meta, tc.userID, tc.driverType, tc.expiresAt)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.wantErr)
				if tc.wantErrIs != nil {
					assert.ErrorIs(t, err, tc.wantErrIs)
				}
			} else {
				require.NoError(t, err)
			}

			tc.assertF(t, tc.storage, tc.driver, tc.tx)
		})
	}
}
