package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// Issuer y Audience acotan para qué sirve un token. Sin ellos, cualquier JWT
	// firmado con el mismo secreto por cualquier otro sistema entraría acá; con
	// login social a la vuelta, además dejan claro cuáles emitimos nosotros y
	// cuáles vienen de un proveedor externo.
	Issuer   = "zports-api"
	Audience = "zports-app"

	// AccessTokenTTL es corto a propósito: un access token no se puede revocar
	// (no hay lista negra), así que el único límite al daño de uno robado es que
	// venza pronto. La sesión larga la sostiene el refresh token.
	AccessTokenTTL = 15 * time.Minute

	// clockLeeway tolera el desfase de reloj entre el teléfono y la máquina de
	// Fly. Sin esto, un celular adelantado unos segundos rechaza tokens recién
	// emitidos.
	clockLeeway = 30 * time.Second
)

// Claims solo identifica al usuario. El rol no vive acá porque es por
// membership (un usuario puede ser manager de un equipo y player de otro);
// se resuelve por request contra memberships, no se cachea en el token.
type Claims struct {
	// UserID duplica el `sub` estándar. Se mantiene porque todos los handlers
	// leen claims.UserID, y `sub` se agrega porque es lo que cualquier
	// herramienta de JWT espera encontrar.
	UserID string `json:"user_id"`
	jwt.RegisteredClaims
}

// Signer emite y valida los access tokens.
//
// Guarda dos secretos para poder rotar JWT_SECRET sin desloguear a nadie: firma
// siempre con el actual y acepta también el anterior. El procedimiento es setear
// el nuevo, mover el viejo a JWT_SECRET_PREVIOUS, y borrarlo pasado el TTL del
// access token.
type Signer struct {
	current  []byte
	previous []byte
}

// NewSigner arma el firmante. previous puede venir vacío, que es el caso normal
// mientras no haya una rotación en curso.
func NewSigner(current, previous string) *Signer {
	s := &Signer{current: []byte(current)}
	if previous != "" {
		s.previous = []byte(previous)
	}
	return s
}

func (s *Signer) NewAccessToken(userID string) (string, error) {
	now := time.Now()
	jti, err := newTokenID()
	if err != nil {
		return "", err
	}
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.current)
}

func (s *Signer) ParseAccessToken(tokenStr string) (*Claims, error) {
	claims, err := parseWithKey(tokenStr, s.current)
	if err == nil {
		return claims, nil
	}
	// Solo se reintenta con el secreto viejo cuando lo que falló fue la firma.
	// Un token vencido o con el issuer equivocado está mal con cualquier clave,
	// y volver a parsearlo sería trabajo al pedo en el camino más caliente.
	if s.previous == nil || !errors.Is(err, jwt.ErrSignatureInvalid) {
		return nil, err
	}
	return parseWithKey(tokenStr, s.previous)
}

func parseWithKey(tokenStr string, key []byte) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenStr,
		&Claims{},
		func(*jwt.Token) (any, error) { return key, nil },
		// WithValidMethods cierra la confusión de algoritmo en la librería, que
		// es más estricto que chequear el tipo a mano: acá HS256 es HS256, y no
		// también HS384 o HS512.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockLeeway),
	)
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	// Un token con `sub` pero sin user_id vendría de otro emisor que comparte el
	// formato; no es nuestro y no identifica a nadie de esta base.
	if claims.UserID == "" {
		return nil, errors.New("token has no user_id")
	}
	return claims, nil
}

func newTokenID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
