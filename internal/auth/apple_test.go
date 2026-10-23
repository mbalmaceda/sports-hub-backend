package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appleKeysServer sirve un JWKS con una clave, como el de Apple, y cuenta
// cuántas veces se lo pidieron.
func appleKeysServer(t *testing.T, kid string, key *rsa.PrivateKey) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kid": kid,
				"kty": "RSA",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func signApple(t *testing.T, kid string, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func newTestAppleVerifier(keysURL string) *appleVerifier {
	v := NewAppleVerifier([]string{"com.zports.app"}).(*appleVerifier)
	v.keysURL = keysURL
	return v
}

func validAppleClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss":            appleIssuer,
		"aud":            "com.zports.app",
		"sub":            "001234.abcd",
		"email":          "abc123@privaterelay.appleid.com",
		"email_verified": "true", // Apple lo manda como texto
		"exp":            time.Now().Add(10 * time.Minute).Unix(),
	}
}

func TestAppleVerifier_AcceptsValidToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	server, hits := appleKeysServer(t, "k1", key)
	v := newTestAppleVerifier(server.URL)

	identity, err := v.Verify(context.Background(), signApple(t, "k1", key, validAppleClaims()))
	require.NoError(t, err)
	assert.Equal(t, "001234.abcd", identity.Subject)
	assert.Equal(t, "abc123@privaterelay.appleid.com", identity.Email)
	assert.True(t, identity.EmailVerified)

	// La segunda vez sale del caché: no se le pega a Apple en cada ingreso.
	_, err = v.Verify(context.Background(), signApple(t, "k1", key, validAppleClaims()))
	require.NoError(t, err)
	assert.Equal(t, int32(1), hits.Load())
}

// Sin el chequeo de audiencia, un token emitido para otra app con Sign in with
// Apple entraría acá con el correo de su dueño.
func TestAppleVerifier_RejectsOtherAudience(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	server, _ := appleKeysServer(t, "k1", key)
	v := newTestAppleVerifier(server.URL)

	claims := validAppleClaims()
	claims["aud"] = "com.otra.app"
	_, err = v.Verify(context.Background(), signApple(t, "k1", key, claims))
	assert.ErrorIs(t, err, ErrInvalidAppleToken)
}

func TestAppleVerifier_RejectsOtherIssuer(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	server, _ := appleKeysServer(t, "k1", key)
	v := newTestAppleVerifier(server.URL)

	claims := validAppleClaims()
	claims["iss"] = "https://accounts.google.com"
	_, err = v.Verify(context.Background(), signApple(t, "k1", key, claims))
	assert.ErrorIs(t, err, ErrInvalidAppleToken)
}

// Firmado con una clave que no es la de Apple, aunque el `kid` coincida.
func TestAppleVerifier_RejectsForeignSignature(t *testing.T) {
	appleKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	server, _ := appleKeysServer(t, "k1", appleKey)
	v := newTestAppleVerifier(server.URL)

	_, err = v.Verify(context.Background(), signApple(t, "k1", otherKey, validAppleClaims()))
	assert.ErrorIs(t, err, ErrInvalidAppleToken)
}

// Un `kid` inventado obliga a volver a pedir las claves, pero no en cada
// request: si no, cualquiera nos hace pegarle a Apple a voluntad.
func TestAppleVerifier_UnknownKidDoesNotRefetchEveryTime(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	server, hits := appleKeysServer(t, "k1", key)
	v := newTestAppleVerifier(server.URL)

	for range 3 {
		_, err = v.Verify(context.Background(), signApple(t, "inventado", key, validAppleClaims()))
		assert.ErrorIs(t, err, ErrInvalidAppleToken)
	}
	assert.Equal(t, int32(1), hits.Load())
}

func TestNewAppleVerifier_NilWithoutAudiences(t *testing.T) {
	assert.Nil(t, NewAppleVerifier(nil))
}
