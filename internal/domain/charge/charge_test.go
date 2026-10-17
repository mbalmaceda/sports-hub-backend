package charge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/charge"
)

// Los valores esperados están verificados contra el mobile: `perPlayerShare`
// (src/core/money.ts) y la mitad de `monthlyMatchEconomics`.
// Las dos implementaciones tienen que dar lo mismo: si divergen, la pantalla
// muestra un monto y el cobro que se guarda es otro.
func TestShares_MatchTheMobileImplementation(t *testing.T) {
	cases := []struct {
		name      string
		total     int64
		perSide   int
		perPlayer int64
		teamShare int64
	}{
		{"fútbol 7", 28000, 7, 2000, 14000},
		{"redondeo hacia arriba", 28000, 11, 1273, 14000},
		{"fútbol 5", 32000, 5, 3200, 16000},
		{"mínimo indivisible", 1, 1, 1, 1},
		{"total impar", 33333, 6, 2778, 16667},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.perPlayer, charge.PerPlayerShare(tc.total, tc.perSide), "cuota por jugador")
			assert.Equal(t, tc.teamShare, charge.TeamShare(tc.total), "mitad del equipo")
		})
	}
}

// Sin costo o sin nómina no hay cuota: nadie queda debiendo un monto inventado.
func TestPerPlayerShare_DegenerateInputsChargeNothing(t *testing.T) {
	assert.Zero(t, charge.PerPlayerShare(0, 7))
	assert.Zero(t, charge.PerPlayerShare(28000, 0))
	// Un costo negativo no debería llegar, pero tampoco puede generar cobros.
	assert.Zero(t, charge.PerPlayerShare(-1000, 7))
}
