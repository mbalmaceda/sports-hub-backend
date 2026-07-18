package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/config"
	"github.com/mbalmaceda/sports-hub-backend/internal/db"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/notification/expo"
	"github.com/mbalmaceda/sports-hub-backend/internal/repository/postgres"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("database error", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("database connected")

	// Repositories
	userRepo := postgres.NewUserRepository(pool)
	tokenRepo := postgres.NewRefreshTokenRepository(pool)

	// Notifier
	notifier := expo.New()
	_ = notifier

	// Handlers
	authHandler := handler.NewAuthHandler(userRepo, tokenRepo, cfg.JWTSecret)

	// Router
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	r.GET("/health", handler.Health)

	r.POST("/auth/register", authHandler.Register)
	r.POST("/auth/login", authHandler.Login)
	r.POST("/auth/refresh", authHandler.Refresh)
	r.POST("/auth/logout", authHandler.Logout)

	// Rutas protegidas — requieren JWT válido
	protected := r.Group("/")
	protected.Use(auth.Middleware(cfg.JWTSecret))
	{
		// próximos handlers van acá
	}

	slog.Info("server starting", "port", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
