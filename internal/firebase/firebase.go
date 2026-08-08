// Package firebase envuelve el Admin SDK con lo que la API necesita de él:
// emitir tokens para que la app entre a Firestore como el mismo usuario que ya
// inició sesión acá, y mantener el espejo de membresías del que cuelgan las
// reglas de seguridad.
//
// La sesión de ZPORTS sigue siendo el JWT propio. Firebase no autentica a nadie:
// se limita a confiar en que este backend ya lo hizo.
package firebase

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/firestore"
	fbsdk "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

// ErrNotConfigured es lo que se devuelve cuando no hay credencial cargada. Se
// distingue de un fallo real porque la respuesta al cliente es otra: no es que
// algo se rompió, es que esta parte todavía no está encendida.
var ErrNotConfigured = errors.New("firebase: no service account configured")

// Firebase agrupa los clientes del Admin SDK sobre una sola app: crear una por
// cliente abriría conexiones de más para hablar con el mismo proyecto.
type Firebase struct {
	auth  *auth.Client
	store *firestore.Client
}

// New devuelve (nil, nil) cuando no hay credencial.
//
// Que falte no es un error: el backend tiene que poder arrancar sin Firebase,
// porque todo lo demás —cuentas, cobros, convocatorias— funciona sin él. Los
// métodos sobre un *Firebase nil contestan ErrNotConfigured en vez de explotar.
func New(ctx context.Context, serviceAccountJSON string) (*Firebase, error) {
	if serviceAccountJSON == "" {
		return nil, nil
	}

	app, err := fbsdk.NewApp(ctx, nil, option.WithCredentialsJSON([]byte(serviceAccountJSON)))
	if err != nil {
		return nil, fmt.Errorf("firebase: init app: %w", err)
	}
	authClient, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase: init auth: %w", err)
	}
	storeClient, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase: init firestore: %w", err)
	}
	return &Firebase{auth: authClient, store: storeClient}, nil
}

// Enabled dice si hay credencial.
func (f *Firebase) Enabled() bool { return f != nil }

// Close suelta las conexiones de Firestore. Sobre un nil no hace nada, para que
// el defer en main no tenga que preguntar.
func (f *Firebase) Close() error {
	if f == nil {
		return nil
	}
	return f.store.Close()
}

// CustomToken emite un token de Firebase para este usuario.
//
// El uid es el id de usuario de Postgres, a propósito: es lo que las reglas de
// Firestore comparan contra el espejo de membresías. Si acá se usara otro
// identificador, las reglas no podrían resolver a qué equipo pertenece quien
// está pidiendo los datos.
//
// El token no lleva roles. Los roles cambian y este token dura una hora; que
// vivan solo en el espejo es lo que permite que degradar a alguien tenga efecto
// inmediato en vez de cuando venza su sesión.
func (f *Firebase) CustomToken(ctx context.Context, userID string) (string, error) {
	if f == nil {
		return "", ErrNotConfigured
	}
	token, err := f.auth.CustomToken(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("firebase: custom token: %w", err)
	}
	return token, nil
}
