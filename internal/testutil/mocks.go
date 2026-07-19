package testutil

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/fee"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/payment"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
)

// --- UserRepository ---

type MockUserRepo struct{ mock.Mock }

func (m *MockUserRepo) FindByID(ctx context.Context, id string) (*user.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*user.User), args.Error(1)
}

func (m *MockUserRepo) FindByEmail(ctx context.Context, email string) (*user.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*user.User), args.Error(1)
}

func (m *MockUserRepo) Create(ctx context.Context, u *user.User) error {
	return m.Called(ctx, u).Error(0)
}

func (m *MockUserRepo) UpdateProfile(ctx context.Context, userID string, upd user.ProfileUpdate) error {
	return m.Called(ctx, userID, upd).Error(0)
}

func (m *MockUserRepo) UpdatePushToken(ctx context.Context, userID, token string) error {
	return m.Called(ctx, userID, token).Error(0)
}

// --- RefreshTokenRepository ---

type MockTokenRepo struct{ mock.Mock }

func (m *MockTokenRepo) Create(ctx context.Context, t *auth.RefreshToken) error {
	return m.Called(ctx, t).Error(0)
}

func (m *MockTokenRepo) FindByHash(ctx context.Context, hash string) (*auth.RefreshToken, error) {
	args := m.Called(ctx, hash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.RefreshToken), args.Error(1)
}

func (m *MockTokenRepo) Delete(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *MockTokenRepo) DeleteByHash(ctx context.Context, hash string) error {
	return m.Called(ctx, hash).Error(0)
}

// --- TeamRepository ---

type MockTeamRepo struct{ mock.Mock }

func (m *MockTeamRepo) FindByID(ctx context.Context, id string) (*team.Team, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*team.Team), args.Error(1)
}

func (m *MockTeamRepo) Create(ctx context.Context, t *team.Team) error {
	return m.Called(ctx, t).Error(0)
}

func (m *MockTeamRepo) List(ctx context.Context) ([]*team.Team, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*team.Team), args.Error(1)
}

func (m *MockTeamRepo) UpdateFeeConfig(ctx context.Context, id string, cfg team.FeeConfig) error {
	return m.Called(ctx, id, cfg).Error(0)
}

// --- MembershipRepository ---

type MockMembershipRepo struct{ mock.Mock }

func (m *MockMembershipRepo) FindByID(ctx context.Context, id string) (*membership.Membership, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*membership.Membership), args.Error(1)
}

func (m *MockMembershipRepo) FindByUserAndTeam(ctx context.Context, userID, teamID string) (*membership.Membership, error) {
	args := m.Called(ctx, userID, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*membership.Membership), args.Error(1)
}

func (m *MockMembershipRepo) ListByTeam(ctx context.Context, teamID string) ([]*membership.TeamMember, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*membership.TeamMember), args.Error(1)
}

func (m *MockMembershipRepo) ListByUser(ctx context.Context, userID string) ([]*membership.TeamMember, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*membership.TeamMember), args.Error(1)
}

func (m *MockMembershipRepo) GetMemberByID(ctx context.Context, id string) (*membership.TeamMember, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*membership.TeamMember), args.Error(1)
}

func (m *MockMembershipRepo) Create(ctx context.Context, ms *membership.Membership) error {
	return m.Called(ctx, ms).Error(0)
}

func (m *MockMembershipRepo) UpdateStatus(ctx context.Context, id string, status membership.Status) error {
	return m.Called(ctx, id, status).Error(0)
}

func (m *MockMembershipRepo) UpdateRole(ctx context.Context, id string, role membership.Role) error {
	return m.Called(ctx, id, role).Error(0)
}

// --- FeeRepository ---

type MockFeeRepo struct{ mock.Mock }

func (m *MockFeeRepo) FindByID(ctx context.Context, id string) (*fee.Obligation, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*fee.Obligation), args.Error(1)
}

func (m *MockFeeRepo) ListByTeamAndPeriod(ctx context.Context, teamID string, year, month int) ([]*fee.Obligation, error) {
	args := m.Called(ctx, teamID, year, month)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*fee.Obligation), args.Error(1)
}

func (m *MockFeeRepo) ListByMembership(ctx context.Context, membershipID string) ([]*fee.Obligation, error) {
	args := m.Called(ctx, membershipID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*fee.Obligation), args.Error(1)
}

func (m *MockFeeRepo) Create(ctx context.Context, o *fee.Obligation) error {
	return m.Called(ctx, o).Error(0)
}

func (m *MockFeeRepo) BulkCreate(ctx context.Context, obligations []*fee.Obligation) (int, error) {
	args := m.Called(ctx, obligations)
	return args.Int(0), args.Error(1)
}

func (m *MockFeeRepo) UpdateStatus(ctx context.Context, id string, status fee.Status, paidAt *time.Time) error {
	return m.Called(ctx, id, status, paidAt).Error(0)
}

// --- PaymentRepository ---

type MockPaymentRepo struct{ mock.Mock }

func (m *MockPaymentRepo) FindByID(ctx context.Context, id string) (*payment.Payment, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*payment.Payment), args.Error(1)
}

func (m *MockPaymentRepo) ListByTeam(ctx context.Context, teamID string) ([]*payment.Payment, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*payment.Payment), args.Error(1)
}

func (m *MockPaymentRepo) FindByObligationID(ctx context.Context, obligationID string) (*payment.Payment, error) {
	args := m.Called(ctx, obligationID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*payment.Payment), args.Error(1)
}

func (m *MockPaymentRepo) Create(ctx context.Context, p *payment.Payment) error {
	return m.Called(ctx, p).Error(0)
}

func (m *MockPaymentRepo) Reverse(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}
