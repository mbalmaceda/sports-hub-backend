package user

import "strings"

// El cuerpo del RUT (sin el verificador): 7 dígitos los más antiguos, 8 los que
// se emiten hoy. Se acepta desde 6 por los pocos que quedan más cortos.
const (
	minRUTBodyDigits = 6
	maxRUTBodyDigits = 8
)

// NormalizeRUT deja el RUT en forma canónica: solo dígitos y el verificador, en
// mayúscula.
//
// "12.345.678-k", "12345678-K" y "12345678K" son la misma persona, y sin esto
// el índice único de `tax_id` los guardaría como tres.
//
// Sirve igual para la identificación tributaria de otros países (CUIT, CPF):
// son todas dígitos, así que limpiar de más no les quita nada.
func NormalizeRUT(value string) string {
	var out strings.Builder
	for _, r := range strings.ToUpper(value) {
		if (r >= '0' && r <= '9') || r == 'K' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// IsValidRUT verifica el dígito verificador con el módulo 11 chileno.
//
// El espejo de esta función vive en el mobile (`src/domain/user/rut.ts`) y las
// dos tienen que aceptar y rechazar exactamente lo mismo: si divergen, o la app
// deja escribir un RUT que el backend rebota, o —peor— muestra un error sobre
// un RUT que el backend habría aceptado.
//
// El vacío no es válido. Si el RUT se puede omitir o no es cosa de cada
// endpoint, no de esta función.
func IsValidRUT(value string) bool {
	clean := NormalizeRUT(value)
	if len(clean) < minRUTBodyDigits+1 || len(clean) > maxRUTBodyDigits+1 {
		return false
	}

	body, dv := clean[:len(clean)-1], clean[len(clean)-1]

	sum, multiplier := 0, 2
	// De derecha a izquierda, multiplicando por la serie 2,3,4,5,6,7 que se repite.
	for i := len(body) - 1; i >= 0; i-- {
		digit := body[i]
		// La K solo puede ser el verificador; en el cuerpo no existe.
		if digit < '0' || digit > '9' {
			return false
		}
		sum += int(digit-'0') * multiplier
		if multiplier == 7 {
			multiplier = 2
		} else {
			multiplier++
		}
	}

	return dv == checkDigit(sum)
}

func checkDigit(sum int) byte {
	switch rest := 11 - sum%11; rest {
	case 11:
		return '0'
	case 10:
		return 'K'
	default:
		return byte('0' + rest)
	}
}
