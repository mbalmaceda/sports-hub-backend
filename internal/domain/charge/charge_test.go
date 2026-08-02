package charge_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
)

func payers(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("m-%d", i)
	}
	return ids
}

// Los valores esperados están verificados contra `splitMatchCost` del mobile.
// Las dos implementaciones tienen que dar lo mismo: si divergen, la pantalla
// muestra un monto y el cobro que se guarda es otro.
func TestSplitMatchCost_MatchesTheMobileImplementation(t *testing.T) {
	cases := []struct {
		name      string
		total     int64
		perSide   int
		payers    int
		perPlayer int64
		teamShare int64
		surplus   int64
	}{
		{"nómina justa", 28000, 7, 7, 2000, 14000, 0},
		{"con dos cambios", 28000, 7, 9, 2000, 14000, 4000},
		{"faltan jugadores", 28000, 7, 3, 2000, 14000, -8000},
		{"redondeo hacia arriba", 28000, 11, 11, 1273, 14000, 3},
		{"fútbol 5", 32000, 5, 5, 3200, 16000, 0},
		{"el doble de la nómina", 30000, 4, 8, 3750, 15000, 15000},
		{"mínimo indivisible", 1, 1, 1, 1, 1, 0},
		{"total impar", 33333, 6, 7, 2778, 16667, 2779},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := charge.SplitMatchCost(tc.total, tc.perSide, payers(tc.payers))

			assert.Equal(t, tc.perPlayer, s.PerPlayer, "cuota por jugador")
			assert.Equal(t, tc.teamShare, s.TeamShare, "mitad del equipo")
			assert.Equal(t, tc.surplus, s.Surplus, "excedente")
			assert.Len(t, s.Lines, tc.payers)

			// Todos pagan lo mismo: el precio sale de la nómina, no de cuántos
			// confirmaron, así que no hay diferencias entre compañeros.
			for _, line := range s.Lines {
				assert.Equal(t, tc.perPlayer, line.Amount)
			}
		})
	}
}

// Sin jugadores, sin costo o sin nómina no se genera deuda contra nadie.
func TestSplitMatchCost_DegenerateInputsChargeNobody(t *testing.T) {
	assert.Empty(t, charge.SplitMatchCost(28000, 7, nil).Lines)
	assert.Empty(t, charge.SplitMatchCost(0, 7, payers(5)).Lines)
	assert.Empty(t, charge.SplitMatchCost(28000, 0, payers(5)).Lines)
	// Un costo negativo no debería llegar, pero tampoco puede generar cobros.
	assert.Empty(t, charge.SplitMatchCost(-1000, 7, payers(5)).Lines)
}

// El excedente es exactamente lo recaudado menos la mitad que le toca al
// equipo. Es el número que decide si sobra plata o si el equipo pone la
// diferencia, así que conviene que no dependa de la vista.
func TestSplitMatchCost_SurplusIsCollectedMinusTeamShare(t *testing.T) {
	s := charge.SplitMatchCost(28000, 7, payers(10))

	collected := s.PerPlayer * 10
	assert.Equal(t, collected-s.TeamShare, s.Surplus)
	assert.Positive(t, s.Surplus, "con 10 pagando una nómina de 7 tiene que sobrar")
}
