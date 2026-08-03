package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/user"
)

type AuthHandler struct {
	users     user.Repository
	tokens    auth.RefreshTokenRepository
	jwtSecret string
}

func NewAuthHandler(users user.Repository, tokens auth.RefreshTokenRepository, jwtSecret string) *AuthHandler {
	return &AuthHandler{users: users, tokens: tokens, jwtSecret: jwtSecret}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req struct {
		Name          string     `json:"name"          binding:"required"`
		Email         string     `json:"email"         binding:"required,email"`
		Password      string     `json:"password"      binding:"required,min=6"`
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
		Email:         req.Email,
		PasswordHash:  string(hash),
		TaxID:         normalizeTaxID(req.TaxID),
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

	accessToken, refreshToken, err := h.issueTokens(c.Request.Context(), u)
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

	u, err := h.users.FindByEmail(c.Request.Context(), req.Email)
	if errors.Is(err, user.ErrNotFound) {
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

	accessToken, refreshToken, err := h.issueTokens(c.Request.Context(), u)
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

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	rt, err := h.tokens.FindByHash(c.Request.Context(), hashToken(req.RefreshToken))
	if errors.Is(err, auth.ErrTokenNotFound) || (err == nil && time.Now().After(rt.ExpiresAt)) {
		if err == nil {
			_ = h.tokens.Delete(c.Request.Context(), rt.ID)
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	u, err := h.users.FindByID(c.Request.Context(), rt.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if err := h.tokens.Delete(c.Request.Context(), rt.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	accessToken, newRefreshToken, err := h.issueTokens(c.Request.Context(), u)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not issue tokens"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": newRefreshToken,
	})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_ = h.tokens.DeleteByHash(c.Request.Context(), hashToken(req.RefreshToken))
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *AuthHandler) issueTokens(ctx context.Context, u *user.User) (accessToken, refreshToken string, err error) {
	accessToken, err = auth.NewAccessToken(u.ID, h.jwtSecret)
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
		TokenHash: hashToken(refreshToken),
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	})
	return
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// normalizeTaxID deja el RUT en forma canónica (solo dígitos y dígito verificador),
// así el índice único detecta duplicados aunque lleguen con puntos o guiones.
func normalizeTaxID(value string) string {
	return strings.NewReplacer(".", "", "-", "", " ", "").Replace(strings.TrimSpace(value))
}

func validDominantSide(v string) bool {
	return v == "" || v == "right" || v == "left" || v == "both"
}
