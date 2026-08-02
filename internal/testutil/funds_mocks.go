package testutil

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/funds"
)

// --- FundsRepository ---

type MockFundsRepo struct{ mock.Mock }

func (m *MockFundsRepo) Set(
	ctx context.Context, teamID string, source funds.Source, amount int64, currency string,
) error {
	return m.Called(ctx, teamID, source, amount, currency).Error(0)
}

func (m *MockFundsRepo) ListByTeam(ctx context.Context, teamID string) ([]*funds.Entry, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*funds.Entry), args.Error(1)
}
