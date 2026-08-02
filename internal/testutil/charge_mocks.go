package testutil

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/onboarding"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
)

// --- ChargeRepository ---

type MockChargeRepo struct{ mock.Mock }

func (m *MockChargeRepo) FindByID(ctx context.Context, id string) (*charge.Charge, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*charge.Charge), args.Error(1)
}

func (m *MockChargeRepo) ListBySource(ctx context.Context, source charge.Source) ([]*charge.Charge, error) {
	args := m.Called(ctx, source)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*charge.Charge), args.Error(1)
}

func (m *MockChargeRepo) ListByMembership(ctx context.Context, membershipID string) ([]*charge.Charge, error) {
	args := m.Called(ctx, membershipID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*charge.Charge), args.Error(1)
}

func (m *MockChargeRepo) CreateForSource(ctx context.Context, in charge.CreateInput) ([]*charge.Charge, error) {
	args := m.Called(ctx, in)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*charge.Charge), args.Error(1)
}

func (m *MockChargeRepo) SubmitReceipt(ctx context.Context, id, receiptURL string, at time.Time) (*charge.Charge, error) {
	args := m.Called(ctx, id, receiptURL)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*charge.Charge), args.Error(1)
}

func (m *MockChargeRepo) Confirm(ctx context.Context, id, confirmedBy string, at time.Time) (*charge.Charge, error) {
	args := m.Called(ctx, id, confirmedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*charge.Charge), args.Error(1)
}

func (m *MockChargeRepo) RejectReceipt(ctx context.Context, id string) (*charge.Charge, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*charge.Charge), args.Error(1)
}

// --- OnboardingRepository ---

type MockOnboardingRepo struct{ mock.Mock }

func (m *MockOnboardingRepo) FindPerson(
	ctx context.Context, method onboarding.LookupMethod, value string,
) (*onboarding.Person, error) {
	args := m.Called(ctx, method, value)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*onboarding.Person), args.Error(1)
}

func (m *MockOnboardingRepo) ListInvitationsForTeam(ctx context.Context, teamID string) ([]*onboarding.TeamInvitation, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*onboarding.TeamInvitation), args.Error(1)
}

func (m *MockOnboardingRepo) ListInvitationsForUser(ctx context.Context, userID string) ([]*onboarding.TeamInvitation, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*onboarding.TeamInvitation), args.Error(1)
}

func (m *MockOnboardingRepo) FindInvitation(ctx context.Context, id string) (*onboarding.TeamInvitation, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*onboarding.TeamInvitation), args.Error(1)
}

func (m *MockOnboardingRepo) CreateInvitation(ctx context.Context, inv *onboarding.TeamInvitation) error {
	return m.Called(ctx, inv).Error(0)
}

func (m *MockOnboardingRepo) RespondToInvitation(
	ctx context.Context, id string, accept bool, at time.Time,
) (*onboarding.TeamInvitation, error) {
	args := m.Called(ctx, id, accept)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*onboarding.TeamInvitation), args.Error(1)
}

func (m *MockOnboardingRepo) ListJoinRequestsForTeam(ctx context.Context, teamID string) ([]*onboarding.JoinRequest, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*onboarding.JoinRequest), args.Error(1)
}

func (m *MockOnboardingRepo) ListJoinRequestsForUser(ctx context.Context, userID string) ([]*onboarding.JoinRequest, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*onboarding.JoinRequest), args.Error(1)
}

func (m *MockOnboardingRepo) FindJoinRequest(ctx context.Context, id string) (*onboarding.JoinRequest, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*onboarding.JoinRequest), args.Error(1)
}

func (m *MockOnboardingRepo) CreateJoinRequest(ctx context.Context, req *onboarding.JoinRequest) error {
	return m.Called(ctx, req).Error(0)
}

func (m *MockOnboardingRepo) RespondToJoinRequest(
	ctx context.Context, id string, accept bool, resolvedBy string, at time.Time,
) (*onboarding.JoinRequest, error) {
	args := m.Called(ctx, id, accept, resolvedBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*onboarding.JoinRequest), args.Error(1)
}

// --- Métodos nuevos de TeamRepository ---
//
// Van acá y no en mocks.go para no mezclar con lo que ya existía; el mock del
// equipo vive allá y estos tres lo completan.

func (m *MockTeamRepo) SearchByName(ctx context.Context, query string) ([]*team.Team, error) {
	args := m.Called(ctx, query)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*team.Team), args.Error(1)
}

func (m *MockTeamRepo) GetBankAccount(ctx context.Context, teamID string) (*team.BankAccount, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*team.BankAccount), args.Error(1)
}

func (m *MockTeamRepo) SaveBankAccount(ctx context.Context, acc *team.BankAccount) error {
	return m.Called(ctx, acc).Error(0)
}
