package handler

import (
	"context"
	"log/slog"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
)

// userIDsForMemberships traduce membresías a usuarios, que es a quien se
// notifica: el token de push está en la cuenta, no en la membresía.
//
// Recibe el plantel que el handler ya cargó para validar los ids, así que no
// vuelve a consultar la base. Una membresía que no esté en ese plantel se
// ignora: si no se pudo validar, tampoco se le manda nada.
func userIDsForMemberships(roster []*membership.TeamMember, membershipIDs []string) []string {
	userByMembership := make(map[string]string, len(roster))
	for _, m := range roster {
		userByMembership[m.MembershipID] = m.UserID
	}

	userIDs := make([]string, 0, len(membershipIDs))
	for _, id := range membershipIDs {
		if userID, ok := userByMembership[id]; ok {
			userIDs = append(userIDs, userID)
		}
	}
	return userIDs
}

/*
managerIDs devuelve a quién avisarle cuando el aviso es para "el equipo".

Diez de los quince flujos apuntan acá —te retaron, te contraofertaron, alguien
pidió sumarse, un parche entró— y ninguno tiene un destinatario individual: la
decisión es del que maneja el equipo, y puede haber más de uno.

`except` es la parte que más se olvida y la que más molesta cuando falta: quien
causó el evento no puede recibir su propio aviso. El manager que carga el
marcador no necesita enterarse de que se cargó el marcador, y el que aprueba una
solicitud no necesita que le avisen que aprobó.

Devolver una lista vacía ante un error es correcto acá: notificar es best-effort
y un fallo leyendo el plantel no puede tumbar la acción que ya se completó.
*/
func managerIDs(
	ctx context.Context,
	memberships membership.Repository,
	teamID string,
	except ...string,
) []string {
	return membership.ManagerUserIDs(rosterOf(ctx, memberships, teamID), except...)
}

// moneyHandlerIDs son los que ven la plata del equipo: manager y tesorero.
//
// Es un grupo más ancho que `managerIDs` a propósito y solo para lo económico.
// El tesorero existe justamente para que el manager no sea el único que mira los
// cobros, y dejarlo afuera del aviso de un pago recibido lo obligaría a entrar a
// buscar si pasó algo.
func moneyHandlerIDs(
	ctx context.Context,
	memberships membership.Repository,
	teamID string,
	except ...string,
) []string {
	return membership.MoneyHandlerUserIDs(rosterOf(ctx, memberships, teamID), except...)
}

/*
teamUserIDs filtra el plantel con un criterio propio.

Lo usa el aviso al plantel, que es el único que va a todos sin mirar el rol. Los
otros dos grupos —managers y los que ven la plata— pasan por el dominio, porque
"quién es manager" es una pregunta sobre roles y esas viven todas en el mismo
archivo.
*/
func teamUserIDs(
	ctx context.Context,
	memberships membership.Repository,
	teamID string,
	include func(*membership.TeamMember) bool,
	except ...string,
) []string {
	excluded := make(map[string]struct{}, len(except))
	for _, id := range except {
		if id != "" {
			excluded[id] = struct{}{}
		}
	}

	userIDs := make([]string, 0, 4)
	for _, m := range rosterOf(ctx, memberships, teamID) {
		if m.Status != membership.StatusActive || m.IsGuest() || !include(m) {
			continue
		}
		if _, skip := excluded[m.UserID]; skip {
			continue
		}
		userIDs = append(userIDs, m.UserID)
	}
	return userIDs
}

// rosterOf trae el plantel para notificar. Devolver vacío ante un error es
// correcto acá: notificar es best-effort y un fallo leyendo el plantel no puede
// tumbar la acción que ya se completó.
func rosterOf(
	ctx context.Context, memberships membership.Repository, teamID string,
) []*membership.TeamMember {
	roster, err := memberships.ListByTeam(ctx, teamID)
	if err != nil {
		slog.Error("could not read the roster to notify the team",
			"error", err, "team_id", teamID)
		return nil
	}
	return roster
}

// teamName resuelve el nombre para el texto del aviso, y nunca falla.
//
// Un aviso que dice "Los Halcones te desafió" vale mucho más que uno que dice
// "un equipo te desafió", pero no lo suficiente como para no mandarlo si la
// consulta se cae: el genérico avisa igual, y el nombre está en la pantalla a
// la que la notificación lleva.
func teamName(ctx context.Context, teams team.Repository, teamID string) string {
	const unknown = "Un equipo"
	if teams == nil {
		return unknown
	}
	t, err := teams.FindByID(ctx, teamID)
	if err != nil || t.Name == "" {
		return unknown
	}
	return t.Name
}

// personName es lo mismo para una persona del plantel. El nombre es el dato que
// hace útil el aviso —"Diego se sumó al equipo" contra "alguien se sumó"— así
// que cuando no se puede leer, conviene que el texto lo esquive en vez de
// mostrar un hueco.
func personName(ctx context.Context, memberships membership.Repository, membershipID string) string {
	const unknown = "Alguien"
	m, err := memberships.GetMemberByID(ctx, membershipID)
	if err != nil || m.FullName == "" {
		return unknown
	}
	return m.FullName
}
