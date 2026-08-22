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

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

func newNotificationHandler() (*handler.NotificationHandler, *testutil.MockNotificationRepo, *testutil.MockPushTokenRepo) {
	history := &testutil.MockNotificationRepo{}
	devices := &testutil.MockPushTokenRepo{}
	// Sin servicio de emisión: estos tests son de lectura y de marcar leído.
	// El aviso al plantel, que sí lo usa, tiene el suyo aparte.
	return handler.NewNotificationHandler(history, devices, nil, nil), history, devices
}

func TestListNotifications_ReturnsFeedWithUnreadCount(t *testing.T) {
	h, history, _ := newNotificationHandler()
	history.On("ListByUser", mock.Anything, "user-1", 50).Return(&notification.Feed{
		Notifications: []*notification.Notification{
			{ID: "n-1", Type: notification.TypeFriendlyChallenged, EntityID: "challenge-1"},
		},
		Unread: 3,
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodGet, "/me/notifications", nil)

	h.List(c)

	require.Equal(t, http.StatusOK, w.Code)
	var body notification.Feed
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 3, body.Unread)
	require.Len(t, body.Notifications, 1)
	assert.Equal(t, "challenge-1", body.Notifications[0].EntityID)
}

// El tope existe para que una cuenta de dos años no traiga miles de filas en un
// request; pedir más de la cuenta se acota en silencio en vez de rechazarse.
func TestListNotifications_CapsTheLimit(t *testing.T) {
	h, history, _ := newNotificationHandler()
	history.On("ListByUser", mock.Anything, "user-1", 200).Return(&notification.Feed{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodGet, "/me/notifications?limit=5000", nil)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	history.AssertCalled(t, "ListByUser", mock.Anything, "user-1", 200)
}

// El id del usuario sale SIEMPRE del token. Es la autorización entera de este
// handler: si el repositorio recibiera otro, cualquiera leería las de otro.
func TestMarkRead_ScopesToTheUserInTheToken(t *testing.T) {
	h, history, _ := newNotificationHandler()
	history.On("MarkRead", mock.Anything, "n-1", "user-1", mock.Anything).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Params = gin.Params{{Key: "notificationId", Value: "n-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/notifications/n-1/read", nil)

	h.MarkRead(c)

	// Un 204 no escribe cuerpo, así que fuera del router el header no se vuelca
	// solo. Mismo empujón que en expense_handler_test.
	c.Writer.WriteHeaderNow()

	assert.Equal(t, http.StatusNoContent, w.Code)
	history.AssertCalled(t, "MarkRead", mock.Anything, "n-1", "user-1", mock.Anything)
}

func TestMarkAllRead_ReturnsHowManyWereMarked(t *testing.T) {
	h, history, _ := newNotificationHandler()
	history.On("MarkAllRead", mock.Anything, "user-1", mock.Anything).Return(7, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodPost, "/me/notifications/read-all", nil)

	h.MarkAllRead(c)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"marked":7}`, w.Body.String())
}

func TestRegisterPushToken_Success(t *testing.T) {
	h, _, devices := newNotificationHandler()
	expoToken := "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]"
	devices.On("Register", mock.Anything, "user-1", expoToken, mock.AnythingOfType("time.Time")).
		Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	body, _ := json.Marshal(map[string]string{"token": expoToken})
	c.Request = httptest.NewRequest(http.MethodPut, "/users/me/push-token", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RegisterPushToken(c)

	assert.Equal(t, http.StatusOK, w.Code)
	devices.AssertCalled(t, "Register", mock.Anything, "user-1", expoToken,
		mock.AnythingOfType("time.Time"))
}

func TestRegisterPushToken_MissingToken(t *testing.T) {
	h, _, _ := newNotificationHandler()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Request = httptest.NewRequest(http.MethodPut, "/users/me/push-token", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RegisterPushToken(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// Marcar leída una que no existe o que es de otro responde igual que marcar la
// propia. Distinguirlas convertiría el endpoint en una forma de averiguar qué
// ids existen, y no hay nada que la app pueda hacer distinto en cada caso.
func TestMarkRead_IsIdempotentAndOpaque(t *testing.T) {
	h, history, _ := newNotificationHandler()
	history.On("MarkRead", mock.Anything, "de-otro", "user-1", mock.Anything).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	contextWithClaims(c, "user-1")
	c.Params = gin.Params{{Key: "notificationId", Value: "de-otro"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/notifications/de-otro/read", nil)

	h.MarkRead(c)

	// Un 204 no escribe cuerpo, así que fuera del router el header no se vuelca
	// solo. Mismo empujón que en expense_handler_test.
	c.Writer.WriteHeaderNow()

	assert.Equal(t, http.StatusNoContent, w.Code)
}
