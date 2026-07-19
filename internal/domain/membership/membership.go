package membership

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("membership not found")

type Status string

const (
	StatusActive    Status = "active"
	StatusInactive  Status = "inactive"
	StatusSuspended Status = "suspended"
)

type Membership struct {
	ID           string
	UserID       string
	TeamID       string
	Status       Status
	JerseyNumber *int
	Position     string
	JoinedAt     time.Time
}

// TeamMember es el read model que combina membership + user,
// mapeado exactamente al tipo TeamMember del mobile.
type TeamMember struct {
	MembershipID string    `json:"membership_id"`
	UserID       string    `json:"user_id"`
	TeamID       string    `json:"team_id"`
	FullName     string    `json:"full_name"`
	AvatarURL    string    `json:"avatar_url,omitempty"`
	Email        string    `json:"email"`
	Phone        string    `json:"phone,omitempty"`
	Role         string    `json:"role"`
	JerseyNumber *int      `json:"jersey_number,omitempty"`
	Position     string    `json:"position,omitempty"`
	Status       Status    `json:"status"`
	JoinedAt     time.Time `json:"joined_at"`
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Membership, error)
	FindByUserAndTeam(ctx context.Context, userID, teamID string) (*Membership, error)
	ListByTeam(ctx context.Context, teamID string) ([]*TeamMember, error)
	GetMemberByID(ctx context.Context, membershipID string) (*TeamMember, error)
	Create(ctx context.Context, m *Membership) error
	UpdateStatus(ctx context.Context, id string, status Status) error
}
