package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

func TestMe_ReturnsCurrentUser(t *testing.T) {
	repo := &testutil.MockUserRepo{}
	h := handler.NewUserHandler(repo)

	u := &user.User{ID: "user-1", Name: "Mirko", Email: "mirko@test.com", Phone: "+56912345678"}
	repo.On("FindByID", mock.Anything, "user-1").Return(u, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodGet, "/users/me", nil)

	h.Me(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Mirko", body["name"])
	assert.Equal(t, "mirko@test.com", body["email"])
	assert.Equal(t, "+56912345678", body["phone"])
	// Campos sensibles no deben aparecer en la respuesta
	assert.Nil(t, body["password_hash"])
	assert.Nil(t, body["push_token"])
}

func TestMe_UserNotFound(t *testing.T) {
	repo := &testutil.MockUserRepo{}
	h := handler.NewUserHandler(repo)

	repo.On("FindByID", mock.Anything, "ghost").Return(nil, user.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "ghost")
	c.Request = httptest.NewRequest(http.MethodGet, "/users/me", nil)

	h.Me(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateProfile_UpdatesAndReturnsUser(t *testing.T) {
	repo := &testutil.MockUserRepo{}
	h := handler.NewUserHandler(repo)

	updated := &user.User{ID: "user-1", Name: "Mirko B.", Phone: "+56999999999", AvatarURL: "https://cdn.example.com/avatar.jpg"}

	repo.On("UpdateProfile", mock.Anything, "user-1", user.ProfileUpdate{
		Name:      "Mirko B.",
		Phone:     "+56999999999",
		AvatarURL: "https://cdn.example.com/avatar.jpg",
	}).Return(nil)
	repo.On("FindByID", mock.Anything, "user-1").Return(updated, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodPatch, "/users/me",
		strings.NewReader(`{"name":"Mirko B.","phone":"+56999999999","avatar_url":"https://cdn.example.com/avatar.jpg"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateProfile(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Mirko B.", body["name"])
	assert.Equal(t, "+56999999999", body["phone"])
	assert.Equal(t, "https://cdn.example.com/avatar.jpg", body["avatar_url"])
}

func TestUpdateProfile_PartialUpdate(t *testing.T) {
	repo := &testutil.MockUserRepo{}
	h := handler.NewUserHandler(repo)

	// Solo actualiza el teléfono — name y avatar_url van vacíos (no sobreescriben)
	repo.On("UpdateProfile", mock.Anything, "user-1", user.ProfileUpdate{
		Name:      "",
		Phone:     "+56911111111",
		AvatarURL: "",
	}).Return(nil)
	repo.On("FindByID", mock.Anything, "user-1").
		Return(&user.User{ID: "user-1", Name: "Nombre original", Phone: "+56911111111"}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodPatch, "/users/me",
		strings.NewReader(`{"phone":"+56911111111"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateProfile(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// El nombre original se preserva
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Nombre original", body["name"])
	repo.AssertExpectations(t)
}

func TestRegisterPushToken_Success(t *testing.T) {
	repo := &testutil.MockUserRepo{}
	h := handler.NewUserHandler(repo)

	expoToken := "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]"
	repo.On("UpdatePushToken", mock.Anything, "user-1", expoToken).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	body, _ := json.Marshal(map[string]string{"token": expoToken})
	c.Request = httptest.NewRequest(http.MethodPut, "/users/me/push-token", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RegisterPushToken(c)

	assert.Equal(t, http.StatusOK, w.Code)
	repo.AssertCalled(t, "UpdatePushToken", mock.Anything, "user-1", expoToken)
}

func TestRegisterPushToken_MissingToken(t *testing.T) {
	h := handler.NewUserHandler(&testutil.MockUserRepo{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodPut, "/users/me/push-token",
		strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RegisterPushToken(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
