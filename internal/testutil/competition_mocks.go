package testutil

import (
	"context"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/friendly"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/match"
)

// --- CompetitionRepository ---

type MockCompetitionRepo struct{ mock.Mock }

func (m *MockCompetitionRepo) FindByID(ctx context.Context, id string) (*competition.Competition, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*competition.Competition), args.Error(1)
}

func (m *MockCompetitionRepo) ListByTeam(ctx context.Context, teamID string) ([]*competition.Competition, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*competition.Competition), args.Error(1)
}

func (m *MockCompetitionRepo) Create(ctx context.Context, c *competition.Competition) error {
	return m.Called(ctx, c).Error(0)
}

func (m *MockCompetitionRepo) UpdateStatus(ctx context.Context, id string, status competition.Status) error {
	return m.Called(ctx, id, status).Error(0)
}

func (m *MockCompetitionRepo) UpdateSchedule(
	ctx context.Context, id string, startAt time.Time, venue string,
) error {
	return m.Called(ctx, id, startAt, venue).Error(0)
}

func (m *MockCompetitionRepo) ListEntries(ctx context.Context, competitionID string) ([]*competition.Entry, error) {
	args := m.Called(ctx, competitionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*competition.Entry), args.Error(1)
}

func (m *MockCompetitionRepo) UpsertEntry(ctx context.Context, e *competition.Entry) error {
	return m.Called(ctx, e).Error(0)
}

func (m *MockCompetitionRepo) FindInvitation(ctx context.Context, id string) (*competition.Invitation, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*competition.Invitation), args.Error(1)
}

func (m *MockCompetitionRepo) ListInvitationsForTeam(ctx context.Context, teamID string) ([]*competition.Invitation, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*competition.Invitation), args.Error(1)
}

func (m *MockCompetitionRepo) CreateInvitation(ctx context.Context, inv *competition.Invitation) error {
	return m.Called(ctx, inv).Error(0)
}

func (m *MockCompetitionRepo) RespondToInvitation(
	ctx context.Context, id string, accept bool, at time.Time,
) (*competition.Invitation, error) {
	args := m.Called(ctx, id, accept, at)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*competition.Invitation), args.Error(1)
}

// --- FriendlyRepository ---

type MockFriendlyRepo struct{ mock.Mock }

func (m *MockFriendlyRepo) FindByID(ctx context.Context, id string) (*friendly.Challenge, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*friendly.Challenge), args.Error(1)
}

func (m *MockFriendlyRepo) ListByTeam(ctx context.Context, teamID string) ([]*friendly.Challenge, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*friendly.Challenge), args.Error(1)
}

func (m *MockFriendlyRepo) Create(ctx context.Context, ch *friendly.Challenge, first *friendly.Proposal) error {
	return m.Called(ctx, ch, first).Error(0)
}

func (m *MockFriendlyRepo) UpdateStatus(ctx context.Context, id string, status friendly.Status) error {
	return m.Called(ctx, id, status).Error(0)
}

func (m *MockFriendlyRepo) ListProposals(ctx context.Context, challengeID string) ([]*friendly.Proposal, error) {
	args := m.Called(ctx, challengeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*friendly.Proposal), args.Error(1)
}

func (m *MockFriendlyRepo) LatestProposal(ctx context.Context, challengeID string) (*friendly.Proposal, error) {
	args := m.Called(ctx, challengeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*friendly.Proposal), args.Error(1)
}

func (m *MockFriendlyRepo) AddProposal(ctx context.Context, p *friendly.Proposal) error {
	return m.Called(ctx, p).Error(0)
}

// --- MatchRepository ---

type MockMatchRepo struct{ mock.Mock }

func (m *MockMatchRepo) FindByID(ctx context.Context, id string) (*match.Match, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*match.Match), args.Error(1)
}

func (m *MockMatchRepo) ListByCompetition(ctx context.Context, competitionID string) ([]*match.Match, error) {
	args := m.Called(ctx, competitionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*match.Match), args.Error(1)
}

func (m *MockMatchRepo) ListByTeam(ctx context.Context, teamID string) ([]*match.Match, error) {
	args := m.Called(ctx, teamID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*match.Match), args.Error(1)
}

func (m *MockMatchRepo) ListByTeamOnDate(ctx context.Context, teamID string, day time.Time) ([]*match.Match, error) {
	args := m.Called(ctx, teamID, day)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*match.Match), args.Error(1)
}

func (m *MockMatchRepo) Create(ctx context.Context, mt *match.Match) error {
	return m.Called(ctx, mt).Error(0)
}

func (m *MockMatchRepo) UpdateStatus(ctx context.Context, id string, status match.Status) error {
	return m.Called(ctx, id, status).Error(0)
}

func (m *MockMatchRepo) ListCallups(ctx context.Context, matchID string) ([]*match.Callup, error) {
	args := m.Called(ctx, matchID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*match.Callup), args.Error(1)
}

func (m *MockMatchRepo) ListCallupsByMembership(ctx context.Context, membershipID string) ([]*match.Callup, error) {
	args := m.Called(ctx, membershipID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*match.Callup), args.Error(1)
}

func (m *MockMatchRepo) CallUp(ctx context.Context, matchID string, membershipIDs []string) ([]*match.Callup, error) {
	args := m.Called(ctx, matchID, membershipIDs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*match.Callup), args.Error(1)
}

func (m *MockMatchRepo) Respond(
	ctx context.Context, matchID, membershipID string, attending bool, at time.Time,
) (*match.Callup, error) {
	args := m.Called(ctx, matchID, membershipID, attending)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*match.Callup), args.Error(1)
}
