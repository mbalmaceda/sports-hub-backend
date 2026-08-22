package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/notification"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
)

/*
NotificationHandler: el historial de novedades de una persona.

Todo acá es del usuario del token y de nadie más. No hay un solo endpoint que
reciba un id de usuario por parámetro, y esa es la autorización entera: los
filtros por `recipient_user_id` del repositorio no son defensa en profundidad,
son lo único que impide leer o marcar las notificaciones de otro.

No hay equipo en la ecuación. Las novedades son personales: quien juega en dos
clubes las ve todas juntas, y quien todavía no tiene equipo —el recién invitado—
tiene que poder abrir esta pantalla igual.
*/
type NotificationHandler struct {
	history notification.Repository
	devices notification.TokenRepository
	// Los dos últimos entran solo por el aviso al plantel, que es la única
	// notificación que nace de un toque en la app y no como efecto de otra cosa.
	notifications *notify.Service
	authz         teamAuthorizer
}

func NewNotificationHandler(
	history notification.Repository,
	devices notification.TokenRepository,
	memberships membership.Repository,
	notifications *notify.Service,
) *NotificationHandler {
	return &NotificationHandler{
		history:       history,
		devices:       devices,
		notifications: notifications,
		authz:         teamAuthorizer{memberships: memberships},
	}
}

/*
defaultLimit y maxLimit acotan el historial.

No hay paginación y por ahora no hace falta: la pantalla es una lista que se lee
de arriba hacia abajo y nadie baja doscientas novedades. El tope existe para que
una cuenta de dos años no traiga miles de filas en un request; el día que alguien
quiera ver más atrás, lo que corresponde es un cursor por `updated_at`, no subir
este número.
*/
const (
	defaultNotificationLimit = 50
	maxNotificationLimit     = 200
)

// List GET /me/notifications
//
// Devuelve las novedades y el no leídas en la misma respuesta. Van juntas
// porque la app las necesita a las dos para pintar la pantalla y el punto de la
// campana, y separarlas eran dos requests para lo mismo.
func (h *NotificationHandler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	limit := defaultNotificationLimit
	if raw := c.Query("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = min(parsed, maxNotificationLimit)
		}
	}

	feed, err := h.history.ListByUser(c.Request.Context(), userID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list notifications"})
		return
	}
	c.JSON(http.StatusOK, feed)
}

// MarkRead POST /notifications/:notificationId/read
//
// Responde 204 aunque la notificación no exista o sea de otro. No es descuido:
// distinguir "no está" de "no es tuya" convierte este endpoint en una forma de
// averiguar qué ids existen, y no hay nada que la app pueda hacer distinto en
// uno u otro caso —marcar leído es idempotente y el resultado deseado es el
// mismo—.
func (h *NotificationHandler) MarkRead(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	err := h.history.MarkRead(c.Request.Context(), c.Param("notificationId"), userID, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not mark the notification as read"})
		return
	}
	c.Status(http.StatusNoContent)
}

// MarkAllRead POST /me/notifications/read-all
// El "Marcar todas como leídas" de la cabecera.
func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	count, err := h.history.MarkAllRead(c.Request.Context(), userID, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not mark the notifications as read"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"marked": count})
}

/*
RegisterPushToken PUT /users/me/push-token

Vive acá y no en UserHandler desde que los dispositivos son tabla propia: el
token dejó de ser un dato del perfil. La ruta no cambió a propósito —la app la
llama en cada arranque— y mudarla habría dejado sin push a todo el que no
actualice.
*/
func (h *NotificationHandler) RegisterPushToken(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.devices.Register(c.Request.Context(), userID, req.Token, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not register push token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

/*
Announce POST /teams/:id/announcements

El aviso del manager a todo el plantel. Es la única notificación que nace de un
toque en la app y no como efecto de otra acción, y la única sin destino: el
mensaje es todo el contenido.

Hasta acá la pantalla existía y no hacía nada —el repositorio del móvil era un
mock que devolvía lo que habría mandado y ahí terminaba—, así que el manager
escribía el aviso, veía "enviado" y no le llegaba a nadie.

Va a los del plantel y no a los invitados: el parche vino a un partido, no al
club, y los avisos del equipo no son suyos.
*/
func (h *NotificationHandler) Announce(c *gin.Context) {
	teamID := c.Param("id")
	me, err := h.authz.requireRole(c, teamID, membership.RoleManager)
	if abortAuthz(c, err) {
		return
	}

	var req struct {
		Message string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the announcement cannot be empty"})
		return
	}
	// El tope no es una política: es el cuerpo de un push, que Expo corta cerca
	// de esto igual. Aceptar más sería guardar un texto que nadie va a ver
	// entero en el teléfono.
	if len(message) > maxAnnouncementLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the announcement is too long"})
		return
	}

	// El que lo escribe queda afuera: no necesita que le llegue su propio aviso.
	recipients := notify.To(teamUserIDs(
		c.Request.Context(), h.authz.memberships, teamID, everyone, me.UserID,
	)...)
	if err := h.notifications.Emit(c.Request.Context(), notify.Event{
		TeamID:     teamID,
		Type:       notification.TypeAnnouncement,
		Title:      "Aviso del equipo",
		Body:       message,
		Recipients: recipients,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not send the announcement"})
		return
	}

	// Se espera el envío en vez de despacharlo en segundo plano, al revés que
	// todo lo demás. Acá el aviso ES la acción: el manager está mirando la
	// pantalla para saber si salió, y un "enviado" optimista que después falla
	// es justamente lo que esta pantalla venía haciendo mal.
	c.JSON(http.StatusOK, gin.H{"sent": len(recipients)})
}

// maxAnnouncementLength es el largo del cuerpo de un push antes de que se corte
// en la bandeja del teléfono.
const maxAnnouncementLength = 500

// everyone incluye a todo el plantel sin mirar el rol. El filtro que importa
// —los invitados quedan afuera— ya está en `teamUserIDs`.
func everyone(*membership.TeamMember) bool { return true }
