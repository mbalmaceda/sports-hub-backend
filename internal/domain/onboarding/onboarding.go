package onboarding

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvitationNotFound  = errors.New("team invitation not found")
	ErrJoinRequestNotFound = errors.New("join request not found")
	// ErrAlreadyAnswered protege de responder dos veces, por ejemplo desde una
	// notificación vieja que quedó en el teléfono.
	ErrAlreadyAnswered = errors.New("this request was already answered")
	ErrPersonNotFound  = errors.New("no registered person matches that data")
	// ErrAlreadyMember evita duplicar la membresía si la persona entró por el
	// otro camino mientras la solicitud estaba pendiente.
	ErrAlreadyMember = errors.New("this person already belongs to the team")
)

type InvitationStatus string

const (
	InvitationSent     InvitationStatus = "sent"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationDeclined InvitationStatus = "declined"
	InvitationExpired  InvitationStatus = "expired"
	InvitationRevoked  InvitationStatus = "revoked"
)

// TeamInvitation: el equipo busca a la persona y la invita. Acepta la persona.
type TeamInvitation struct {
	ID              string           `json:"id"`
	TeamID          string           `json:"team_id"`
	InvitedByUserID string           `json:"invited_by_user_id"`
	UserID          string           `json:"user_id"`
	Status          InvitationStatus `json:"status"`
	CreatedAt       time.Time        `json:"created_at"`
	RespondedAt     *time.Time       `json:"responded_at,omitempty"`
}

type JoinRequestStatus string

const (
	JoinPending   JoinRequestStatus = "pending"
	JoinAccepted  JoinRequestStatus = "accepted"
	JoinDeclined  JoinRequestStatus = "declined"
	JoinCancelled JoinRequestStatus = "cancelled"
)

// JoinRequest: la persona encuentra al equipo y pide entrar. Acepta el equipo.
type JoinRequest struct {
	ID          string            `json:"id"`
	TeamID      string            `json:"team_id"`
	UserID      string            `json:"user_id"`
	Message     string            `json:"message,omitempty"`
	Status      JoinRequestStatus `json:"status"`
	CreatedAt   time.Time         `json:"created_at"`
	RespondedAt *time.Time        `json:"responded_at,omitempty"`
	ResolvedBy  *string           `json:"resolved_by,omitempty"`
	// Datos de quien pide, embebidos para que la pantalla del manager no tenga
	// que hacer una consulta por fila.
	FullName string `json:"full_name,omitempty"`
	Email    string `json:"email,omitempty"`
	TaxID    string `json:"tax_id,omitempty"`
}

// LookupMethod es por qué campo se busca a la persona.
type LookupMethod string

const (
	LookupByTaxID LookupMethod = "tax_id"
	LookupByEmail LookupMethod = "email"
)

// Person es el resultado de la búsqueda para invitar. Expone lo mínimo: nombre,
// el dato por el que se la buscó, y si ya tiene equipo. No es un directorio
// navegable, es la confirmación de que a quien se quiere invitar existe.
type Person struct {
	UserID   string `json:"user_id"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	TaxID    string `json:"tax_id,omitempty"`
	HasTeam  bool   `json:"has_team"`
}

type Repository interface {
	// FindPerson busca por coincidencia exacta, normalizando puntos y guiones.
	// No hay búsqueda parcial por nombre a propósito: convertiría el padrón de
	// usuarios en algo navegable por cualquier manager.
	FindPerson(ctx context.Context, method LookupMethod, value string) (*Person, error)

	ListInvitationsForTeam(ctx context.Context, teamID string) ([]*TeamInvitation, error)
	ListInvitationsForUser(ctx context.Context, userID string) ([]*TeamInvitation, error)
	FindInvitation(ctx context.Context, id string) (*TeamInvitation, error)
	CreateInvitation(ctx context.Context, inv *TeamInvitation) error
	// RespondToInvitation resuelve la invitación y, si se acepta, crea la
	// membresía en la misma transacción.
	RespondToInvitation(ctx context.Context, id string, accept bool, at time.Time) (*TeamInvitation, error)

	ListJoinRequestsForTeam(ctx context.Context, teamID string) ([]*JoinRequest, error)
	ListJoinRequestsForUser(ctx context.Context, userID string) ([]*JoinRequest, error)
	FindJoinRequest(ctx context.Context, id string) (*JoinRequest, error)
	CreateJoinRequest(ctx context.Context, req *JoinRequest) error
	// RespondToJoinRequest resuelve la solicitud y, si se acepta, da de alta la
	// membresía en la misma transacción. Partirlo dejaría solicitudes aceptadas
	// sin jugador en el plantel.
	RespondToJoinRequest(ctx context.Context, id string, accept bool, resolvedBy string, at time.Time) (*JoinRequest, error)
}
