package auth

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/api/idtoken"
)

// ErrInvalidGoogleToken es cualquier motivo por el que el ID token no sirve:
// firma mala, vencido, o emitido para otra app. Se colapsan en uno solo porque
// al cliente se le contesta lo mismo en los tres casos —decir cuál falló le
// dice a un atacante qué está probando— y el detalle queda en el log.
var ErrInvalidGoogleToken = errors.New("invalid google id token")

/*
GoogleIdentity es lo único que este backend necesita de Google.

Deliberadamente flaco: quién es (`Subject`), su correo y si Google lo dio por
verificado. El resto del ID token —foto, dominio, locale— no se usa, y traerlo
sería guardar datos de una persona porque estaban a mano.
*/
type GoogleIdentity struct {
	// Subject es el id estable de la cuenta de Google. Hoy no se persiste —el
	// vínculo se hace por correo— pero es lo que habría que guardar el día que
	// alguien cambie de correo sin perder la cuenta.
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

/*
GoogleVerifier valida el ID token que la app trae de Google.

Es una interfaz para que el handler se pueda probar sin salir a la red: la
implementación real pega contra las claves públicas de Google.
*/
type GoogleVerifier interface {
	Verify(ctx context.Context, idToken string) (*GoogleIdentity, error)
}

/*
googleVerifier valida contra Google, con la lista de audiencias permitidas.

Hay más de una a propósito y no por indecisión: la app Android y la de iOS
reciben el token con un `aud` distinto —el client id de cada plataforma— y las
dos son legítimas. Lo que **no** se puede hacer es saltear el chequeo de
audiencia: sin él, un ID token emitido para cualquier otra app de Google entra
acá y se lleva la sesión del dueño de ese correo.
*/
type googleVerifier struct {
	audiences []string
}

// NewGoogleVerifier devuelve nil si no hay audiencias configuradas, que es la
// forma de decir "esta instalación no tiene Google Sign-In". El handler lo
// traduce a un 503 en vez de intentar validar contra nada.
func NewGoogleVerifier(audiences []string) GoogleVerifier {
	if len(audiences) == 0 {
		return nil
	}
	return &googleVerifier{audiences: audiences}
}

func (v *googleVerifier) Verify(ctx context.Context, idToken string) (*GoogleIdentity, error) {
	// Se prueba audiencia por audiencia porque la librería valida contra una
	// sola. Pasar "" saltearía el chequeo, que es justo lo que no se puede.
	var lastErr error
	for _, audience := range v.audiences {
		payload, err := idtoken.Validate(ctx, idToken, audience)
		if err != nil {
			lastErr = err
			continue
		}
		return identityFrom(payload), nil
	}
	return nil, fmt.Errorf("%w: %v", ErrInvalidGoogleToken, lastErr)
}

func identityFrom(payload *idtoken.Payload) *GoogleIdentity {
	claim := func(key string) string {
		value, _ := payload.Claims[key].(string)
		return value
	}
	verified, _ := payload.Claims["email_verified"].(bool)

	return &GoogleIdentity{
		Subject:       payload.Subject,
		Email:         claim("email"),
		EmailVerified: verified,
		Name:          claim("name"),
	}
}
