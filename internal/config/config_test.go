package config

import "testing"

/*
El enlace de invitación es lo único que sale de este servidor hacia afuera por
WhatsApp, así que la regla de qué origen se acepta se prueba sola.

Lo que hay que no romper es el par: https siempre, y http únicamente cuando el
enlace no puede salir de la red local. Aflojar el segundo caso de más —un http
a un host público— serviría enlaces que en el teléfono abren el navegador en
vez de la app, sin que nada avise.
*/
func TestValidatePublicBaseURL(t *testing.T) {
	valid := []string{
		"https://sports-hub-backend.fly.dev",
		"https://zports.cl",
		// Desarrollo: el emulador contra la máquina y el teléfono por wifi.
		"http://localhost:8085",
		"http://127.0.0.1:8085",
		"http://192.168.100.180:8085",
		"http://10.0.2.2:8085",
		"http://172.16.4.9:8085",
	}
	for _, raw := range valid {
		if err := validatePublicBaseURL(raw); err != nil {
			t.Errorf("validatePublicBaseURL(%q) = %v, se esperaba que pasara", raw, err)
		}
	}

	invalid := []string{
		// Un host público por http: el caso que la regla existe para frenar.
		"http://sports-hub-backend.fly.dev",
		"http://zports.cl",
		// Un dominio que resuelve a una IP privada no alcanza: acá se mira lo
		// que está escrito, no lo que devuelve el DNS.
		"http://mi-servidor.local",
		// Sin esquema no hay nada que verificar.
		"sports-hub-backend.fly.dev",
		"ftp://zports.cl",
		"zports://invite",
	}
	for _, raw := range invalid {
		if err := validatePublicBaseURL(raw); err == nil {
			t.Errorf("validatePublicBaseURL(%q) = nil, se esperaba error", raw)
		}
	}
}
