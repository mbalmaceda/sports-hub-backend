package testutil

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/teamlink"
)

type MockTeamLinkRepo struct {
	mock.Mock
}

func (m *MockTeamLinkRepo) Create(ctx context.Context, inv *teamlink.Invite, tokenHash string) error {
	args := m.Called(ctx, inv, tokenHash)
	// El repositorio real completa el id al insertar.
	if inv.ID == "" {
		inv.ID = "link-1"
	}
	return args.Error(0)
}

func (m *MockTeamLinkRepo) FindByTokenHash(ctx context.Context, tokenHash string) (*teamlink.Invite, error) {
	args := m.Called(ctx, tokenHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*teamlink.Invite), args.Error(1)
}

func (m *MockTeamLinkRepo) FindByID(ctx context.Context, id string) (*teamlink.Invite, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*teamlink.Invite), args.Error(1)
}

func (m *MockTeamLinkRepo) ListByTeam(ctx context.Context, teamID string) ([]*teamlink.Invite, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*teamlink.Invite), args.Error(1)
}

func (m *MockTeamLinkRepo) Revoke(ctx context.Context, id string, at time.Time) error {
	return m.Called(ctx, id, at).Error(0)
}

func (m *MockTeamLinkRepo) Redeem(
	ctx context.Context, tokenHash, userID string, now time.Time,
) (*teamlink.AcceptResult, error) {
	args := m.Called(ctx, tokenHash, userID, now)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*teamlink.AcceptResult), args.Error(1)
}
