package handler

import "github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"

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
