package handler_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

func init() {
	gin.SetMode(gin.TestMode)
}

const testSecret = "test-secret-32-bytes-long-enough!"

func newAuthHandler(userRepo *testutil.MockUserRepo, tokenRepo *testutil.MockTokenRepo) *handler.AuthHandler {
	return handler.NewAuthHandler(
		userRepo, tokenRepo,
		auth.NewSigner(testSecret, ""),
		nil, // sin límite por cuenta: lo que se prueba acá es el flujo, no el limitador
		slog.New(slog.DiscardHandler),
	)
}

// expectIssueTokens registra lo que toca la emisión de un par de tokens en un
// login nuevo: crear el eslabón y podar las sesiones viejas.
func expectIssueTokens(tokenRepo *testutil.MockTokenRepo) {
	tokenRepo.On("Create", mock.Anything, mock.AnythingOfType("*auth.RefreshToken")).Return(nil)
	tokenRepo.On("TrimFamilies", mock.Anything, mock.AnythingOfType("string"), auth.MaxSessionsPerUser).
		Return(nil)
}

func TestRegister_Success(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(userRepo, tokenRepo)

	userRepo.On("Create", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)
	expectIssueTokens(tokenRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"name":"Test","email":"test@test.com","password":"secret12345"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Register(c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body["access_token"])
	assert.NotEmpty(t, body["refresh_token"])
	assert.NotNil(t, body["user"])
}

func TestRegister_ValidationError(t *testing.T) {
	h := newAuthHandler(&testutil.MockUserRepo{}, &testutil.MockTokenRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"name":"X","email":"not-an-email","password":"123"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Register(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegister_DuplicateEmail(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	h := newAuthHandler(userRepo, &testutil.MockTokenRepo{})

	userRepo.On("Create", mock.Anything, mock.AnythingOfType("*user.User")).
		Return(assert.AnError)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"name":"Test","email":"dup@test.com","password":"secret12345"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Register(c)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestRegister_WithProfile(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(userRepo, tokenRepo)

	userRepo.On("Create", mock.Anything, mock.MatchedBy(func(u *user.User) bool {
		return u.Name == "Mirko" &&
			u.TaxID == "123456785" &&
			u.FavoriteSport == "football" &&
			u.City == "Santiago" &&
			u.DominantSide == "right" &&
			u.HeightCm != nil && *u.HeightCm == 175 &&
			u.WeightKg != nil && *u.WeightKg == 70.5 &&
			u.BirthDate != nil && u.BirthDate.Format("2006-01-02") == "1998-07-12"
	})).Return(nil)
	expectIssueTokens(tokenRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{
			"name":"Mirko","email":"mirko@test.com","password":"secret12345",
			"tax_id":"12.345.678-5","favorite_sport":"football","height_cm":175,
			"weight_kg":70.5,"birth_date":"1998-07-12","city":"Santiago","dominant_side":"right"
		}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Register(c)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestRegister_InvalidTaxID(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	h := newAuthHandler(userRepo, &testutil.MockTokenRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// El verificador de 12.345.678 es 5, no 9.
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"name":"Test","email":"t@test.com","password":"secret12345","tax_id":"12.345.678-9"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Register(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "tax_id is not a valid RUT", body["error"])
	// Con el RUT malo no se llega a crear nada.
	userRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestRegister_DuplicateTaxID(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	h := newAuthHandler(userRepo, &testutil.MockTokenRepo{})

	userRepo.On("Create", mock.Anything, mock.AnythingOfType("*user.User")).
		Return(user.ErrTaxIDTaken)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"name":"Test","email":"dup@test.com","password":"secret12345","tax_id":"12345678-5"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Register(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "tax id already registered", body["error"])
}

func TestRegister_InvalidDominantSide(t *testing.T) {
	h := newAuthHandler(&testutil.MockUserRepo{}, &testutil.MockTokenRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"name":"Test","email":"test@test.com","password":"secret12345","dominant_side":"north"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Register(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLogin_Success(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(userRepo, tokenRepo)

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret12345"), bcrypt.MinCost)
	u := &user.User{ID: "user-1", Email: "test@test.com", PasswordHash: string(hash)}

	userRepo.On("FindByEmail", mock.Anything, "test@test.com").Return(u, nil)
	expectIssueTokens(tokenRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(`{"email":"test@test.com","password":"secret12345"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Login(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body["access_token"])
}

func TestLogin_WrongPassword(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	h := newAuthHandler(userRepo, &testutil.MockTokenRepo{})

	hash, _ := bcrypt.GenerateFromPassword([]byte("correct"), bcrypt.MinCost)
	u := &user.User{ID: "user-1", Email: "test@test.com", PasswordHash: string(hash)}

	userRepo.On("FindByEmail", mock.Anything, "test@test.com").Return(u, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(`{"email":"test@test.com","password":"wrong"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Login(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestLogin_UserNotFound(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	h := newAuthHandler(userRepo, &testutil.MockTokenRepo{})

	userRepo.On("FindByEmail", mock.Anything, "ghost@test.com").Return(nil, user.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(`{"email":"ghost@test.com","password":"any"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Login(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// postRefresh arma el request de /auth/refresh y lo corre.
func postRefresh(h *handler.AuthHandler, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"refresh_token": token})
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Refresh(c)
	return w
}

func TestRefresh_Success(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(userRepo, tokenRepo)

	rt := &auth.RefreshToken{
		ID:        "rt-1",
		UserID:    "user-1",
		FamilyID:  "fam-1",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	u := &user.User{ID: "user-1", Email: "test@test.com"}

	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).Return(rt, nil)
	tokenRepo.On("MarkUsed", mock.Anything, "rt-1", mock.AnythingOfType("time.Time")).Return(nil)
	userRepo.On("FindByID", mock.Anything, "user-1").Return(u, nil)
	tokenRepo.On("Create", mock.Anything, mock.AnythingOfType("*auth.RefreshToken")).Return(nil)

	w := postRefresh(h, "valid-token")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["access_token"])
	assert.NotEmpty(t, resp["refresh_token"])

	// El token nuevo hereda la cadena del que rotó: es la misma sesión.
	created := tokenRepo.Calls[len(tokenRepo.Calls)-1].Arguments.Get(1).(*auth.RefreshToken)
	assert.Equal(t, "fam-1", created.FamilyID)
	// Rotar no abre una sesión nueva, así que no hay nada que podar.
	tokenRepo.AssertNotCalled(t, "TrimFamilies", mock.Anything, mock.Anything, mock.Anything)
}

func TestRefresh_InvalidToken(t *testing.T) {
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(&testutil.MockUserRepo{}, tokenRepo)

	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).
		Return(nil, auth.ErrTokenNotFound)

	assert.Equal(t, http.StatusUnauthorized, postRefresh(h, "invalid").Code)
}

// Un token ya rotado que reaparece pasada la ventana de gracia es una
// reutilización: se cae la cadena entera, no solo esa request.
func TestRefresh_ReuseRevokesFamily(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(userRepo, tokenRepo)

	usedAt := time.Now().Add(-2 * auth.ReuseGrace)
	rt := &auth.RefreshToken{
		ID:        "rt-1",
		UserID:    "user-1",
		FamilyID:  "fam-1",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		UsedAt:    &usedAt,
	}

	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).Return(rt, nil)
	tokenRepo.On("RevokeFamily", mock.Anything, "fam-1", mock.AnythingOfType("time.Time")).Return(nil)

	assert.Equal(t, http.StatusUnauthorized, postRefresh(h, "stolen-token").Code)

	tokenRepo.AssertCalled(t, "RevokeFamily", mock.Anything, "fam-1", mock.AnythingOfType("time.Time"))
	// No se emite nada: la sesión murió.
	tokenRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// Dos refresh en paralelo son lo normal cuando varias requests reciben 401 a la
// vez. Dentro de la gracia no es un robo y no puede desloguear a nadie.
func TestRefresh_ConcurrentWithinGraceDoesNotRevoke(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(userRepo, tokenRepo)

	usedAt := time.Now().Add(-time.Second)
	rt := &auth.RefreshToken{
		ID:        "rt-1",
		UserID:    "user-1",
		FamilyID:  "fam-1",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		UsedAt:    &usedAt,
	}
	u := &user.User{ID: "user-1", Email: "test@test.com"}

	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).Return(rt, nil)
	userRepo.On("FindByID", mock.Anything, "user-1").Return(u, nil)
	tokenRepo.On("Create", mock.Anything, mock.AnythingOfType("*auth.RefreshToken")).Return(nil)

	w := postRefresh(h, "same-token-twice")

	assert.Equal(t, http.StatusOK, w.Code)
	tokenRepo.AssertNotCalled(t, "RevokeFamily", mock.Anything, mock.Anything, mock.Anything)
}

// Si la carrera se pierde en el UPDATE en vez de en el SELECT, el resultado
// tiene que ser el mismo: un token nuevo, no un deslogueo.
func TestRefresh_LostMarkUsedRaceIssuesToken(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(userRepo, tokenRepo)

	rt := &auth.RefreshToken{
		ID:        "rt-1",
		UserID:    "user-1",
		FamilyID:  "fam-1",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	u := &user.User{ID: "user-1", Email: "test@test.com"}

	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).Return(rt, nil)
	tokenRepo.On("MarkUsed", mock.Anything, "rt-1", mock.AnythingOfType("time.Time")).
		Return(auth.ErrTokenNotFound)
	userRepo.On("FindByID", mock.Anything, "user-1").Return(u, nil)
	tokenRepo.On("Create", mock.Anything, mock.AnythingOfType("*auth.RefreshToken")).Return(nil)

	assert.Equal(t, http.StatusOK, postRefresh(h, "racing-token").Code)
	tokenRepo.AssertNotCalled(t, "RevokeFamily", mock.Anything, mock.Anything, mock.Anything)
}

func TestRefresh_RevokedFamilyIsRejected(t *testing.T) {
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(&testutil.MockUserRepo{}, tokenRepo)

	revokedAt := time.Now().Add(-time.Minute)
	rt := &auth.RefreshToken{
		ID:        "rt-1",
		UserID:    "user-1",
		FamilyID:  "fam-1",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		RevokedAt: &revokedAt,
	}
	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).Return(rt, nil)

	assert.Equal(t, http.StatusUnauthorized, postRefresh(h, "revoked-token").Code)
	tokenRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestRefresh_ExpiredIsRejected(t *testing.T) {
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(&testutil.MockUserRepo{}, tokenRepo)

	rt := &auth.RefreshToken{
		ID:        "rt-1",
		UserID:    "user-1",
		FamilyID:  "fam-1",
		ExpiresAt: time.Now().Add(-time.Hour),
	}
	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).Return(rt, nil)

	assert.Equal(t, http.StatusUnauthorized, postRefresh(h, "expired-token").Code)
	tokenRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

// Cerrar sesión en un dispositivo mata la cadena entera, no solo el último
// eslabón: si no, un token intermedio filtrado seguiría sirviendo.
func TestLogout_RevokesFamily(t *testing.T) {
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(&testutil.MockUserRepo{}, tokenRepo)

	rt := &auth.RefreshToken{ID: "rt-1", UserID: "user-1", FamilyID: "fam-1"}
	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).Return(rt, nil)
	tokenRepo.On("RevokeFamily", mock.Anything, "fam-1", mock.AnythingOfType("time.Time")).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"refresh_token": "some-token"})
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Logout(c)

	assert.Equal(t, http.StatusOK, w.Code)
	tokenRepo.AssertCalled(t, "RevokeFamily", mock.Anything, "fam-1", mock.AnythingOfType("time.Time"))
}

// Un token inexistente no puede distinguirse de uno válido desde afuera: si
// respondiera distinto, probar tokens diría cuáles existen.
func TestLogout_UnknownTokenStillReturnsOK(t *testing.T) {
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(&testutil.MockUserRepo{}, tokenRepo)

	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).
		Return(nil, auth.ErrTokenNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"refresh_token": "never-existed"})
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Logout(c)

	assert.Equal(t, http.StatusOK, w.Code)
}
