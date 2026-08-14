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

// Role es el rol del usuario dentro de ESTE equipo. No es global:
// un usuario puede ser manager de un equipo y solo player de otro.
type Role string

const (
	RolePlayer    Role = "player"
	RoleTreasurer Role = "treasurer"
	RoleManager   Role = "manager"
)

// Kind responde si la persona es del equipo o está de paso por un partido.
//
// Es ortogonal a Role y a PlaysAsPlayer, y las tres tienen que seguir
// separadas: Role es qué puede hacer con el equipo, PlaysAsPlayer si ocupa un
// lugar en el plantel, Kind si pertenece. Un invitado juega y paga su cuota de
// cancha —PlaysAsPlayer es TRUE—, lo que no es es del club.
type Kind string

const (
	KindMember Kind = "member"
	// KindGuest es el "parche": vino a completar una convocatoria puntual. No
	// se le generan cuotas mensuales y solo ve el partido al que lo invitaron.
	KindGuest Kind = "guest"
)

type Membership struct {
	ID     string
	UserID string
	TeamID string
	Role   Role
	// Kind separa al plantel de los invitados de un partido. Vacío se lee como
	// KindMember: es lo que devuelve un backend sin la 002 aplicada, y el
	// default de la columna.
	Kind Kind
	// PlaysAsPlayer indica si esta membresía ocupa un lugar en el plantel: se la
	// convoca a los partidos y se le generan cuotas. Es independiente del rol,
	// porque el manager que además juega es lo habitual en el fútbol amateur.
	PlaysAsPlayer bool
	Status        Status
	JerseyNumber  *int
	Position      string
	JoinedAt      time.Time
}

// IsGuest indica si esta membresía es un invitado de un partido.
//
// Se pregunta acá y no comparando el campo suelto por la misma razón que
// `isActiveSquadMember` en el mobile: la regla vive en un lugar y el día que
// aparezca un tercer tipo de vínculo no hay que salir a cazar comparaciones.
func (m Membership) IsGuest() bool {
	return m.Kind == KindGuest
}

// IsGuest, para el read model. Misma regla que la de Membership.
func (m TeamMember) IsGuest() bool {
	return m.Kind == KindGuest
}

// DefaultPlaysAsPlayer resuelve el valor cuando quien crea la membresía no lo
// dice: todos juegan menos el manager. Es la regla que aplicaba el código antes
// de que la columna existiera, y la que deja el alta de siempre sin cambios.
func DefaultPlaysAsPlayer(role Role) bool {
	return role != RoleManager
}

// TeamMember es el read model que combina membership + user,
// mapeado exactamente al tipo TeamMember del mobile.
//
// Email y Phone vienen vacíos cuando el TeamMember sale de un listado de
// plantel: los datos de contacto son de a uno y salen de GetMemberByID. No es
// un olvido del mapeo, es la query —ver `rosterListQuery`—, así que asumir que
// están poblados en cualquier lista es asumir de más.
type TeamMember struct {
	MembershipID  string    `json:"membership_id"`
	UserID        string    `json:"user_id"`
	TeamID        string    `json:"team_id"`
	FullName      string    `json:"full_name"`
	AvatarURL     string    `json:"avatar_url,omitempty"`
	Email         string    `json:"email,omitempty"`
	Phone         string    `json:"phone,omitempty"`
	Role          Role      `json:"role"`
	Kind          Kind      `json:"kind"`
	PlaysAsPlayer bool      `json:"plays_as_player"`
	JerseyNumber  *int      `json:"jersey_number,omitempty"`
	Position      string    `json:"position,omitempty"`
	Status        Status    `json:"status"`
	JoinedAt      time.Time `json:"joined_at"`
}

type Repository interface {
	FindByID(ctx context.Context, id string) (*Membership, error)
	FindByUserAndTeam(ctx context.Context, userID, teamID string) (*Membership, error)
	ListByTeam(ctx context.Context, teamID string) ([]*TeamMember, error)
	ListByUser(ctx context.Context, userID string) ([]*TeamMember, error)
	GetMemberByID(ctx context.Context, membershipID string) (*TeamMember, error)
	Create(ctx context.Context, m *Membership) error
	UpdateStatus(ctx context.Context, id string, status Status) error
	UpdateRole(ctx context.Context, id string, role Role) error
	/*
		PromoteGuest convierte a un invitado de un partido en miembro del plantel.

		Es el final feliz del "parche": jugó tres sábados y el equipo lo quiere
		adentro. A partir de acá cuenta como plantel, se le generan cuotas
		mensuales y ve el equipo entero, así que es una decisión del manager y no
		algo que pase solo.

		Solo opera sobre kind='guest'. Sobre alguien que ya es del plantel no
		hace nada: no es un error, es que ya está donde tiene que estar.
	*/
	PromoteGuest(ctx context.Context, id string) error
}
