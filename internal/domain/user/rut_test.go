package user_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
)

// Los casos están verificados contra `isValidRut` del mobile
// (`src/domain/user/rut.ts`). Las dos implementaciones tienen que dar lo mismo:
// si divergen, la app rebota un RUT que el backend aceptaría, o al revés.
func TestIsValidRUT(t *testing.T) {
	cases := []struct {
		name  string
		value string
		valid bool
	}{
		{"con puntos y guion", "12.345.678-5", true},
		{"solo con guion", "12345678-5", true},
		{"pelado", "123456785", true},
		{"todos unos", "11.111.111-1", true},
		{"cuerpo de siete dígitos", "5.126.663-3", true},
		{"verificador K", "15.000.005-K", true},
		{"verificador k minúscula", "15000005-k", true},
		{"verificador equivocado", "12.345.678-9", false},
		{"verificador K donde va un 5", "12.345.678-K", false},
		{"K en el cuerpo", "12.34K.678-5", false},
		{"cuerpo demasiado corto", "1-9", false},
		{"cuerpo demasiado largo", "123456789-5", false},
		{"letras", "abc", false},
		{"vacío", "", false},
		{"solo el verificador", "5", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.valid, user.IsValidRUT(tc.value))
		})
	}
}

// El verificador que devuelve el módulo 11 tiene once resultados posibles y hay
// que poder escribirlos todos, incluidos el 0 y la K.
func TestIsValidRUT_AcceptsEveryCheckDigit(t *testing.T) {
	seen := map[byte]bool{}

	// Cuerpos consecutivos: entre cien seguidos salen los once verificadores.
	// Cada cuerpo tiene uno solo, así que esto también comprueba que no acepte
	// dos verificadores distintos para el mismo RUT.
	for n := 15000000; n < 15000100; n++ {
		body := strconv.Itoa(n)
		matches := 0
		for _, dv := range []byte("0123456789K") {
			if user.IsValidRUT(body + string(dv)) {
				seen[dv] = true
				matches++
			}
		}
		assert.Equal(t, 1, matches, "el cuerpo %s aceptó %d verificadores", body, matches)
	}

	for _, dv := range []byte("0123456789K") {
		assert.True(t, seen[dv], "ningún cuerpo dio el verificador %q", string(dv))
	}
}

func TestNormalizeRUT(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{"12.345.678-5", "123456785"},
		{"12345678-5", "123456785"},
		{"  12345678-5  ", "123456785"},
		{"15000005-k", "15000005K"},
		{"", ""},
	}

	for _, tc := range cases {
		assert.Equal(t, tc.want, user.NormalizeRUT(tc.value), tc.value)
	}
}
