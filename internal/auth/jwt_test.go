package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
)

const (
	secretA = "secret-a-that-is-32-bytes-long!!"
	secretB = "secret-b-that-is-32-bytes-long!!"
)

func TestAccessToken_RoundTrip(t *testing.T) {
	signer := auth.NewSigner(secretA, "")

	token, err := signer.NewAccessToken("user-1")
	require.NoError(t, err)

	claims, err := signer.ParseAccessToken(token)
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.UserID)
	assert.Equal(t, "user-1", claims.Subject)
	assert.Equal(t, auth.Issuer, claims.Issuer)
	assert.Contains(t, claims.Audience, auth.Audience)
	assert.NotEmpty(t, claims.ID, "el jti sirve para correlacionar en los logs")
}

func TestAccessToken_RejectsOtherSecret(t *testing.T) {
	token, err := auth.NewSigner(secretA, "").NewAccessToken("user-1")
	require.NoError(t, err)

	_, err = auth.NewSigner(secretB, "").ParseAccessToken(token)
	assert.Error(t, err)
}

// La rotación de JWT_SECRET no puede desloguear a nadie: mientras el secreto
// viejo esté en JWT_SECRET_PREVIOUS, los tokens ya emitidos siguen valiendo.
func TestAccessToken_AcceptsPreviousSecret(t *testing.T) {
	oldToken, err := auth.NewSigner(secretA, "").NewAccessToken("user-1")
	require.NoError(t, err)

	rotated := auth.NewSigner(secretB, secretA)

	claims, err := rotated.ParseAccessToken(oldToken)
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.UserID)

	// Y lo que emite desde ahora va firmado con el nuevo.
	newToken, err := rotated.NewAccessToken("user-1")
	require.NoError(t, err)
	_, err = auth.NewSigner(secretB, "").ParseAccessToken(newToken)
	assert.NoError(t, err)
}

// Un token con la firma correcta pero emitido por otro sistema no entra: es lo
// que aportan iss y aud, y lo que va a separar nuestros tokens de los de
// Google/Apple cuando exista el login social.
func TestAccessToken_RejectsForeignIssuerAndAudience(t *testing.T) {
	signer := auth.NewSigner(secretA, "")

	for _, tc := range []struct {
		name     string
		issuer   string
		audience string
	}{
		{"otro emisor", "someone-else", auth.Audience},
		{"otra audiencia", auth.Issuer, "another-app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := signWith(t, secretA, jwt.MapClaims{
				"user_id": "user-1",
				"sub":     "user-1",
				"iss":     tc.issuer,
				"aud":     tc.audience,
				"exp":     time.Now().Add(time.Hour).Unix(),
			})
			_, err := signer.ParseAccessToken(token)
			assert.Error(t, err)
		})
	}
}

func TestAccessToken_RejectsExpired(t *testing.T) {
	token := signWith(t, secretA, jwt.MapClaims{
		"user_id": "user-1",
		"iss":     auth.Issuer,
		"aud":     auth.Audience,
		"exp":     time.Now().Add(-time.Hour).Unix(),
	})
	_, err := auth.NewSigner(secretA, "").ParseAccessToken(token)
	assert.ErrorIs(t, err, jwt.ErrTokenExpired)
}

// Sin exp un token no vence nunca. WithExpirationRequired lo rechaza.
func TestAccessToken_RejectsMissingExpiration(t *testing.T) {
	token := signWith(t, secretA, jwt.MapClaims{
		"user_id": "user-1",
		"iss":     auth.Issuer,
		"aud":     auth.Audience,
	})
	_, err := auth.NewSigner(secretA, "").ParseAccessToken(token)
	assert.Error(t, err)
}

// Confusión de algoritmo: el mismo secreto con HS512 en vez de HS256.
func TestAccessToken_RejectsOtherSigningMethod(t *testing.T) {
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
		"user_id": "user-1",
		"iss":     auth.Issuer,
		"aud":     auth.Audience,
		"exp":     time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(secretA))
	require.NoError(t, err)

	_, err = auth.NewSigner(secretA, "").ParseAccessToken(token)
	assert.Error(t, err)
}

// Un token bien firmado y con nuestro iss/aud pero sin user_id no identifica a
// nadie de esta base.
func TestAccessToken_RejectsMissingUserID(t *testing.T) {
	token := signWith(t, secretA, jwt.MapClaims{
		"sub": "user-1",
		"iss": auth.Issuer,
		"aud": auth.Audience,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	_, err := auth.NewSigner(secretA, "").ParseAccessToken(token)
	assert.Error(t, err)
}

func signWith(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)
	return token
}
