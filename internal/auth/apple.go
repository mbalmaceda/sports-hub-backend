package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidAppleToken es cualquier motivo por el que el identity token no
// sirve. Igual que con Google, se colapsan en uno: al cliente se le contesta
// lo mismo y el detalle queda en el log.
var ErrInvalidAppleToken = errors.New("invalid apple identity token")

const (
	appleIssuer  = "https://appleid.apple.com"
	appleKeysURL = "https://appleid.apple.com/auth/keys"

	// Apple rota sus claves sin avisar. Un `kid` desconocido obliga a volver a
	// pedirlas, pero no más de una vez por minuto: si no, cualquiera que mande
	// tokens con un `kid` inventado nos hace pegarle a Apple en cada request.
	appleKeysMaxAge       = 24 * time.Hour
	appleKeysRefetchFloor = time.Minute
	appleKeysFetchTimeout = 5 * time.Second
)

/*
AppleIdentity es lo que este backend usa de Apple: lo mismo que de Google.

El nombre **no** viene acá. Apple no lo pone en el identity token: lo entrega
una sola vez, en el primer ingreso, del lado del teléfono. Por eso la app lo
manda aparte en el body, y el handler solo lo usa al crear la cuenta.
*/
type AppleIdentity struct {
	// Subject es el id estable de la persona para esta app. Como en Google, hoy
	// no se persiste: el vínculo es por correo.
	Subject       string
	Email         string
	EmailVerified bool
}

// AppleVerifier valida el identity token de Sign in with Apple. Es interfaz por
// lo mismo que GoogleVerifier: probar el handler sin salir a la red.
type AppleVerifier interface {
	Verify(ctx context.Context, identityToken string) (*AppleIdentity, error)
}

/*
appleVerifier valida contra las claves públicas de Apple.

La audiencia es el bundle id de la app (`com.zports.app`), no un client id de
OAuth: así lo emite Apple para el ingreso nativo. Igual que en Google, no
chequearla dejaría entrar un token emitido para cualquier otra app con Sign in
with Apple.
*/
type appleVerifier struct {
	audiences []string
	client    *http.Client
	keysURL   string

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewAppleVerifier devuelve nil sin audiencias: esta instalación no tiene Sign
// in with Apple y el handler contesta 503.
func NewAppleVerifier(audiences []string) AppleVerifier {
	if len(audiences) == 0 {
		return nil
	}
	return &appleVerifier{
		audiences: audiences,
		client:    &http.Client{Timeout: appleKeysFetchTimeout},
		keysURL:   appleKeysURL,
	}
}

func (v *appleVerifier) Verify(ctx context.Context, identityToken string) (*AppleIdentity, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(identityToken, claims,
		func(token *jwt.Token) (any, error) {
			kid, _ := token.Header["kid"].(string)
			return v.key(ctx, kid)
		},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(appleIssuer),
		jwt.WithAudience(v.audiences...),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAppleToken, err)
	}

	subject, _ := claims.GetSubject()
	email, _ := claims["email"].(string)
	return &AppleIdentity{
		Subject:       subject,
		Email:         email,
		EmailVerified: claimTrue(claims["email_verified"]),
	}, nil
}

// claimTrue lee un booleano que Apple a veces manda como `true` y a veces como
// `"true"`, según la versión del token.
func claimTrue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true"
	default:
		return false
	}
}

func (v *appleVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if key, ok := v.keys[kid]; ok && time.Since(v.fetchedAt) < appleKeysMaxAge {
		return key, nil
	}
	if time.Since(v.fetchedAt) >= appleKeysRefetchFloor {
		keys, err := v.fetchKeys(ctx)
		if err != nil {
			return nil, err
		}
		v.keys = keys
		v.fetchedAt = time.Now()
	}
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("unknown apple key id %q", kid)
}

func (v *appleVerifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.keysURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch apple keys: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch apple keys: status %d", res.StatusCode)
	}

	var body struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode apple keys: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(body.Keys))
	for _, jwk := range body.Keys {
		if jwk.Kty != "RSA" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(jwk.N)
		e, errE := base64.RawURLEncoding.DecodeString(jwk.E)
		if errN != nil || errE != nil {
			continue
		}
		keys[jwk.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: int(new(big.Int).SetBytes(e).Int64()),
		}
	}
	return keys, nil
}
