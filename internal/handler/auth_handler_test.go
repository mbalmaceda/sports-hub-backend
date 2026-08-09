package handler_test

import (
	"encoding/json"
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

func newAuthHandler(userRepo *testutil.MockUserRepo, tokenRepo *testutil.MockTokenRepo) *handler.AuthHandler {
	return handler.NewAuthHandler(userRepo, tokenRepo, "test-secret-32-bytes-long-enough!")
}

func TestRegister_Success(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(userRepo, tokenRepo)

	userRepo.On("Create", mock.Anything, mock.AnythingOfType("*user.User")).Return(nil)
	tokenRepo.On("Create", mock.Anything, mock.AnythingOfType("*auth.RefreshToken")).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{"name":"Test","email":"test@test.com","password":"secret123"}`))
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
		strings.NewReader(`{"name":"Test","email":"dup@test.com","password":"secret123"}`))
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
	tokenRepo.On("Create", mock.Anything, mock.AnythingOfType("*auth.RefreshToken")).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/register",
		strings.NewReader(`{
			"name":"Mirko","email":"mirko@test.com","password":"secret123",
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
		strings.NewReader(`{"name":"Test","email":"t@test.com","password":"secret123","tax_id":"12.345.678-9"}`))
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
		strings.NewReader(`{"name":"Test","email":"dup@test.com","password":"secret123","tax_id":"12345678-5"}`))
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
		strings.NewReader(`{"name":"Test","email":"test@test.com","password":"secret123","dominant_side":"north"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Register(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLogin_Success(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(userRepo, tokenRepo)

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	u := &user.User{ID: "user-1", Email: "test@test.com", PasswordHash: string(hash)}

	userRepo.On("FindByEmail", mock.Anything, "test@test.com").Return(u, nil)
	tokenRepo.On("Create", mock.Anything, mock.AnythingOfType("*auth.RefreshToken")).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(`{"email":"test@test.com","password":"secret123"}`))
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

func TestRefresh_Success(t *testing.T) {
	userRepo := &testutil.MockUserRepo{}
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(userRepo, tokenRepo)

	rt := &auth.RefreshToken{
		ID:        "rt-1",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	u := &user.User{ID: "user-1", Email: "test@test.com"}

	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).Return(rt, nil)
	userRepo.On("FindByID", mock.Anything, "user-1").Return(u, nil)
	tokenRepo.On("Delete", mock.Anything, "rt-1").Return(nil)
	tokenRepo.On("Create", mock.Anything, mock.AnythingOfType("*auth.RefreshToken")).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"refresh_token": "valid-token"})
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Refresh(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["access_token"])
	assert.NotEmpty(t, resp["refresh_token"])
}

func TestRefresh_InvalidToken(t *testing.T) {
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(&testutil.MockUserRepo{}, tokenRepo)

	tokenRepo.On("FindByHash", mock.Anything, mock.AnythingOfType("string")).
		Return(nil, auth.ErrTokenNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"refresh_token": "invalid"})
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Refresh(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestLogout_Success(t *testing.T) {
	tokenRepo := &testutil.MockTokenRepo{}
	h := newAuthHandler(&testutil.MockUserRepo{}, tokenRepo)

	tokenRepo.On("DeleteByHash", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]string{"refresh_token": "some-token"})
	c.Request = httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Logout(c)

	assert.Equal(t, http.StatusOK, w.Code)
}
