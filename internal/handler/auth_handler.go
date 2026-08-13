package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
	"github.com/mbalmaceda/sports-hub-backend/internal/middleware"
)

// maxPasswordBytes es el límite de bcrypt. Pasado ese punto ignora el resto en
// silencio, así que dos contraseñas largas con el mismo prefijo entrarían igual.
// Se rechaza explícitamente en vez de dejar que el usuario crea que su
// contraseña de 100 caracteres lo protege más.
const maxPasswordBytes = 72

// dummyPasswordHash sirve para gastar el mismo tiempo cuando el email no existe.
// Ver el comentario en Login.
var dummyPasswordHash []byte

func init() {
	h, err := bcrypt.GenerateFromPassword([]byte("timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		panic("bcrypt: " + err.Error())
	}
	dummyPasswordHash = h
}

// AttemptLimiter es el limitador por cuenta. Es una interfaz y no el tipo
// concreto para que el handler no dependa del paquete de middleware más de lo
// necesario, y para poder pasar nil en los tests.
type AttemptLimiter interface {
	Allow(key string) (bool, time.Duration)
}

type AuthHandler struct {
	users  user.Repository
	tokens auth.RefreshTokenRepository
	signer *auth.Signer
	// loginLimiter cuenta por email, no por IP. Es el que frena el credential
	// stuffing repartido entre muchas IPs contra una misma cuenta, que el
	// limitador por IP del router no ve.
	loginLimiter AttemptLimiter
	logger       *slog.Logger
}

func NewAuthHandler(
	users user.Repository,
	tokens auth.RefreshTokenRepository,
	signer *auth.Signer,
	loginLimiter AttemptLimiter,
	logger *slog.Logger,
) *AuthHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthHandler{
		users:        users,
		tokens:       tokens,
		signer:       signer,
		loginLimiter: loginLimiter,
		logger:       logger,
	}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req struct {
		Name          string     `json:"name"          binding:"required"`
		Email         string     `json:"email"         binding:"required,email"`
		Password      string     `json:"password"      binding:"required,min=10"`
		TaxID         string     `json:"tax_id"`
		FavoriteSport string     `json:"favorite_sport"`
		HeightCm      *int       `json:"height_cm"`
		WeightKg      *float64   `json:"weight_kg"`
		BirthDate     *user.Date `json:"birth_date"`
		Alias         string     `json:"alias"`
		City          string     `json:"city"`
		DominantSide  string     `json:"dominant_side"`
		Bio           string     `json:"bio"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Password) > maxPasswordBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be at most 72 bytes"})
		return
	}
	// El RUT sigue siendo opcional acá, pero si viene tiene que ser uno de
	// verdad: es la llave con la que después un manager busca a esta persona
	// para invitarla, y un RUT inventado la deja imposible de encontrar. La app
	// ya lo valida, así que esto ataja a cualquier otro cliente.
	if req.TaxID != "" && !user.IsValidRUT(req.TaxID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tax_id is not a valid RUT"})
		return
	}
	if !validDominantSide(req.DominantSide) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dominant_side must be one of right, left, both"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	u := &user.User{
		Name:          req.Name,
		Email:         user.NormalizeEmail(req.Email),
		PasswordHash:  string(hash),
		TaxID:         user.NormalizeRUT(req.TaxID),
		FavoriteSport: req.FavoriteSport,
		HeightCm:      req.HeightCm,
		WeightKg:      req.WeightKg,
		BirthDate:     req.BirthDate,
		Alias:         req.Alias,
		City:          req.City,
		DominantSide:  req.DominantSide,
		Bio:           req.Bio,
	}
	if err := h.users.Create(c.Request.Context(), u); err != nil {
		if errors.Is(err, user.ErrTaxIDTaken) {
			c.JSON(http.StatusConflict, gin.H{"error": "tax id already registered"})
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
		return
	}

	accessToken, refreshToken, err := h.issueTokens(c.Request.Context(), u, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not issue tokens"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"user":          u,
	})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Email    string `json:"email"    binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	email := user.NormalizeEmail(req.Email)

	// El límite por cuenta va antes de tocar la base: si no, el propio intento
	// rechazado seguiría costando una consulta y un bcrypt.
	if h.loginLimiter != nil {
		if ok, retryAfter := h.loginLimiter.Allow("login:" + email); !ok {
			middleware.AbortTooManyRequests(c, retryAfter)
			return
		}
	}

	u, err := h.users.FindByEmail(c.Request.Context(), email)
	if errors.Is(err, user.ErrNotFound) {
		// Comparar contra un hash de descarte antes de responder. Sin esto, el
		// "no existe" vuelve en microsegundos y la contraseña equivocada tarda
		// los ~80 ms de bcrypt: medir la diferencia dice si un email está
		// registrado, aunque el mensaje de error sea el mismo en los dos casos.
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(req.Password))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	accessToken, refreshToken, err := h.issueTokens(c.Request.Context(), u, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not issue tokens"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"user":          u,
	})
}

// Refresh rota el refresh token y, si detecta que se reutilizó uno ya rotado,
// mata la cadena de sesión entera.
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	now := time.Now()

	rt, err := h.tokens.FindByHash(ctx, hashToken(req.RefreshToken))
	if errors.Is(err, auth.ErrTokenNotFound) {
		h.abortInvalidRefresh(c)
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Familia ya revocada, o token vencido: no hay nada que rotar.
	if rt.RevokedAt != nil || !now.Before(rt.ExpiresAt) {
		h.abortInvalidRefresh(c)
		return
	}

	if rt.UsedAt != nil {
		if !rt.WithinGrace(now) {
			// Reutilización fuera de la ventana de concurrencia. O el token se
			// filtró, o alguien está reproduciendo una sesión vieja: se cae toda
			// la cadena, incluida la del atacante si es que ya rotó.
			if err := h.tokens.RevokeFamily(ctx, rt.FamilyID, now); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			h.logger.Warn("refresh token reuse detected, session family revoked",
				"user_id", rt.UserID,
				"family_id", rt.FamilyID,
				"client_ip", c.ClientIP(),
				"used_at", rt.UsedAt,
			)
			h.abortInvalidRefresh(c)
			return
		}
		// Dentro de la gracia: es la app pidiendo dos refresh a la vez. Se le da
		// un eslabón nuevo de la misma cadena sin revocar nada.
		h.issueRotated(c, rt)
		return
	}

	if err := h.tokens.MarkUsed(ctx, rt.ID, now); err != nil {
		if errors.Is(err, auth.ErrTokenNotFound) {
			// Otra request marcó el token entre el SELECT y el UPDATE. Es la
			// misma concurrencia de arriba, ganada por microsegundos.
			h.issueRotated(c, rt)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	h.issueRotated(c, rt)
}

// issueRotated emite el par nuevo dentro de la cadena del token recibido.
func (h *AuthHandler) issueRotated(c *gin.Context, rt *auth.RefreshToken) {
	ctx := c.Request.Context()

	u, err := h.users.FindByID(ctx, rt.UserID)
	if errors.Is(err, user.ErrNotFound) {
		// La cuenta se dio de baja con la sesión abierta. Antes esto devolvía
		// 500; es un 401, y de paso se limpia lo que quede de la cadena.
		_ = h.tokens.RevokeFamily(ctx, rt.FamilyID, time.Now())
		h.abortInvalidRefresh(c)
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	accessToken, refreshToken, err := h.issueTokens(ctx, u, rt.FamilyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not issue tokens"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	})
}

// Logout cierra la sesión del dispositivo: revoca la cadena entera y no solo el
// último eslabón, porque si no un token intermedio filtrado seguiría sirviendo.
//
// Responde 200 pase lo que pase: decir si el token existía sería confirmarle a
// quien lo pruebe que tiene uno válido.
func (h *AuthHandler) Logout(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if rt, err := h.tokens.FindByHash(c.Request.Context(), hashToken(req.RefreshToken)); err == nil {
		_ = h.tokens.RevokeFamily(c.Request.Context(), rt.FamilyID, time.Now())
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// LogoutAll cierra todas las sesiones del usuario. Va protegida por el
// middleware de auth: quien la llama ya probó ser el dueño de la cuenta.
func (h *AuthHandler) LogoutAll(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if err := h.tokens.RevokeAllForUser(c.Request.Context(), userID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// abortInvalidRefresh da siempre la misma respuesta para token inexistente,
// vencido, revocado o reutilizado: distinguirlos le diría al atacante en qué
// estado quedó el token que probó.
func (h *AuthHandler) abortInvalidRefresh(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token"})
}

// issueTokens emite el par. familyID vacío significa login nuevo: la base crea
// la cadena. Con familyID, el token hereda la cadena del que se acaba de rotar.
func (h *AuthHandler) issueTokens(
	ctx context.Context, u *user.User, familyID string,
) (accessToken, refreshToken string, err error) {
	accessToken, err = h.signer.NewAccessToken(u.ID)
	if err != nil {
		return
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return
	}
	refreshToken = hex.EncodeToString(raw)
	err = h.tokens.Create(ctx, &auth.RefreshToken{
		UserID:    u.ID,
		FamilyID:  familyID,
		TokenHash: hashToken(refreshToken),
		ExpiresAt: time.Now().Add(auth.RefreshTokenTTL),
	})
	if err != nil {
		return
	}
	// Solo en logins nuevos: es cuando aparece una cadena más. Al rotar no
	// cambia la cantidad de sesiones, así que no hay nada que podar.
	//
	// Que falle no invalida el login que acaba de salir bien; queda en el log.
	if familyID == "" {
		if trimErr := h.tokens.TrimFamilies(ctx, u.ID, auth.MaxSessionsPerUser); trimErr != nil {
			h.logger.Warn("could not trim old sessions", "user_id", u.ID, "error", trimErr)
		}
	}
	return
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func validDominantSide(v string) bool {
	return v == "" || v == "right" || v == "left" || v == "both"
}
