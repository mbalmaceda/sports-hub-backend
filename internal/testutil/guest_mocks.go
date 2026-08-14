package testutil

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/guest"
)

type MockGuestInviteRepo struct {
	mock.Mock
}

func (m *MockGuestInviteRepo) Create(ctx context.Context, inv *guest.Invite, tokenHash string) error {
	args := m.Called(ctx, inv, tokenHash)
	// El repositorio real completa el id al insertar; el mock hace lo mismo
	// para que el handler pueda devolverlo.
	if inv.ID == "" {
		inv.ID = "invite-1"
	}
	return args.Error(0)
}

func (m *MockGuestInviteRepo) FindByTokenHash(ctx context.Context, tokenHash string) (*guest.Invite, error) {
	args := m.Called(ctx, tokenHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*guest.Invite), args.Error(1)
}

func (m *MockGuestInviteRepo) FindByID(ctx context.Context, id string) (*guest.Invite, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*guest.Invite), args.Error(1)
}

func (m *MockGuestInviteRepo) ListByMatch(ctx context.Context, matchID string) ([]*guest.Invite, error) {
	args := m.Called(ctx, matchID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*guest.Invite), args.Error(1)
}

func (m *MockGuestInviteRepo) Revoke(ctx context.Context, id string, at time.Time) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockGuestInviteRepo) Redeem(
	ctx context.Context, tokenHash, userID string, now time.Time,
) (*guest.AcceptResult, error) {
	args := m.Called(ctx, tokenHash, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*guest.AcceptResult), args.Error(1)
}
