package testutil

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/venue"
)

// --- FriendlySearchRepository ---

type MockFriendlySearchRepo struct{ mock.Mock }

func (m *MockFriendlySearchRepo) Create(ctx context.Context, s *friendly.Search) error {
	return m.Called(ctx, s).Error(0)
}

func (m *MockFriendlySearchRepo) FindByID(ctx context.Context, id string) (*friendly.Search, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*friendly.Search), args.Error(1)
}

func (m *MockFriendlySearchRepo) ListNearby(
	ctx context.Context, lat, lng, radiusKm float64, viewerTeamID string, now time.Time,
) ([]*friendly.Search, error) {
	args := m.Called(ctx, lat, lng, radiusKm, viewerTeamID, now)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*friendly.Search), args.Error(1)
}

func (m *MockFriendlySearchRepo) Close(
	ctx context.Context, id string, status friendly.SearchStatus, keepChallengeID string,
) ([]*friendly.Challenge, error) {
	args := m.Called(ctx, id, status, keepChallengeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*friendly.Challenge), args.Error(1)
}

func (m *MockFriendlySearchRepo) ExpireStale(ctx context.Context, now time.Time) error {
	return m.Called(ctx, now).Error(0)
}

// --- VenueRepository ---

type MockVenueRepo struct{ mock.Mock }

func (m *MockVenueRepo) FindByID(ctx context.Context, id string) (*venue.Venue, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*venue.Venue), args.Error(1)
}

func (m *MockVenueRepo) Search(
	ctx context.Context, query string, lat, lng *float64, limit int,
) ([]*venue.Venue, error) {
	args := m.Called(ctx, query, lat, lng, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*venue.Venue), args.Error(1)
}

func (m *MockVenueRepo) FindOrCreate(ctx context.Context, v *venue.Venue) (*venue.Venue, bool, error) {
	args := m.Called(ctx, v)
	if args.Get(0) == nil {
		return nil, false, args.Error(2)
	}
	return args.Get(0).(*venue.Venue), args.Bool(1), args.Error(2)
}

func (m *MockVenueRepo) List(ctx context.Context, f venue.Filter) ([]*venue.Venue, error) {
	args := m.Called(ctx, f)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*venue.Venue), args.Error(1)
}

func (m *MockVenueRepo) Create(ctx context.Context, v *venue.Venue) error {
	return m.Called(ctx, v).Error(0)
}

func (m *MockVenueRepo) Update(ctx context.Context, id string, u venue.Update) (*venue.Venue, error) {
	args := m.Called(ctx, id, u)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*venue.Venue), args.Error(1)
}

func (m *MockVenueRepo) Merge(ctx context.Context, fromID, intoID string) error {
	return m.Called(ctx, fromID, intoID).Error(0)
}
