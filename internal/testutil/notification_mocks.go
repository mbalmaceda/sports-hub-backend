package testutil

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
)

// --- notification.Repository ---

type MockNotificationRepo struct{ mock.Mock }

func (m *MockNotificationRepo) CreateMany(
	ctx context.Context, drafts []notification.Draft,
) ([]*notification.Notification, error) {
	args := m.Called(ctx, drafts)
	if n, ok := args.Get(0).([]*notification.Notification); ok {
		return n, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockNotificationRepo) ListByUser(
	ctx context.Context, userID string, limit int,
) (*notification.Feed, error) {
	args := m.Called(ctx, userID, limit)
	if f, ok := args.Get(0).(*notification.Feed); ok {
		return f, args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockNotificationRepo) MarkRead(
	ctx context.Context, id, userID string, at time.Time,
) error {
	return m.Called(ctx, id, userID, at).Error(0)
}

func (m *MockNotificationRepo) MarkAllRead(
	ctx context.Context, userID string, at time.Time,
) (int64, error) {
	args := m.Called(ctx, userID, at)
	return int64(args.Int(0)), args.Error(1)
}

// --- notification.TokenRepository ---

type MockPushTokenRepo struct{ mock.Mock }

func (m *MockPushTokenRepo) Register(
	ctx context.Context, userID, token string, at time.Time,
) error {
	return m.Called(ctx, userID, token, at).Error(0)
}

func (m *MockPushTokenRepo) DevicesByUserIDs(
	ctx context.Context, userIDs []string,
) ([]notification.Device, error) {
	args := m.Called(ctx, userIDs)
	if d, ok := args.Get(0).([]notification.Device); ok {
		return d, args.Error(1)
	}
	return nil, args.Error(1)
}
