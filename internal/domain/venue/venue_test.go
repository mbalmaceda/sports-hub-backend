package venue

import "testing"

func TestNameKey(t *testing.T) {
	cases := map[string]string{
		"Complejo Maipú":        "complejo maipu",
		"  complejo   MAIPU  ":  "complejo maipu",
		"Estadio Ñuñoa":         "estadio nunoa",
		"Cancha\tSan  Dámaso\n": "cancha san damaso",
	}
	for in, want := range cases {
		if got := NameKey(in); got != want {
			t.Errorf("NameKey(%q) = %q, se esperaba %q", in, got, want)
		}
	}
}

func TestValidCoordinate(t *testing.T) {
	if !ValidCoordinate(-33.5106, -70.7570) {
		t.Error("Maipú debería ser una coordenada válida")
	}
	if ValidCoordinate(0, 0) {
		t.Error("(0, 0) es lo que manda un cliente sin coordenada")
	}
	if ValidCoordinate(91, 0) || ValidCoordinate(0, 181) {
		t.Error("fuera de rango")
	}
}
