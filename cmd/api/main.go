package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/gin-contrib/cors"
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
	teamRepo := postgres.NewTeamRepository(pool)
	rosterRepo := postgres.NewRosterRepository(pool)
	feeRepo := postgres.NewFeeRepository(pool)
	paymentRepo := postgres.NewPaymentRepository(pool)

	// Notifier
	notifier := expo.New()
	_ = notifier

	// Handlers
	authHandler := handler.NewAuthHandler(userRepo, tokenRepo, cfg.JWTSecret)
	userHandler := handler.NewUserHandler(userRepo)
	teamHandler := handler.NewTeamHandler(teamRepo, rosterRepo)
	rosterHandler := handler.NewRosterHandler(rosterRepo)
	feeHandler := handler.NewFeeHandler(feeRepo, rosterRepo, teamRepo)
	paymentHandler := handler.NewPaymentHandler(paymentRepo, feeRepo)

	// Router
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:8081", "http://localhost:8082", "http://localhost:19006", "http://localhost:19000"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: false,
	}))

	r.GET("/health", handler.Health)

	r.POST("/auth/register", authHandler.Register)
	r.POST("/auth/login", authHandler.Login)
	r.POST("/auth/refresh", authHandler.Refresh)
	r.POST("/auth/logout", authHandler.Logout)

	// Rutas protegidas — requieren JWT válido
	protected := r.Group("/")
	protected.Use(auth.Middleware(cfg.JWTSecret))
	{
		protected.GET("/users/me", userHandler.Me)
		protected.PATCH("/users/me", userHandler.UpdateProfile)
		protected.PUT("/users/me/push-token", userHandler.RegisterPushToken)

		protected.GET("/me/memberships", rosterHandler.ListMine)

		protected.GET("/teams", teamHandler.List)
		protected.POST("/teams", teamHandler.Create)
		protected.GET("/teams/:id", teamHandler.GetByID)
		protected.PATCH("/teams/:id/fee-config", teamHandler.UpdateFeeConfig)

		protected.GET("/teams/:id/roster", rosterHandler.ListByTeam)
		protected.POST("/teams/:id/roster", rosterHandler.AddMember)
		protected.GET("/teams/:id/roster/:membershipId", rosterHandler.GetMember)
		protected.GET("/memberships/:membershipId", rosterHandler.GetMember)
		protected.PATCH("/teams/:id/roster/:membershipId/status", rosterHandler.UpdateStatus)
		protected.PATCH("/teams/:id/roster/:membershipId/role", rosterHandler.UpdateRole)

		protected.GET("/teams/:id/fees", feeHandler.ListByTeamAndPeriod)
		protected.POST("/teams/:id/generate-fees", feeHandler.Generate)
		protected.GET("/teams/:id/payments", paymentHandler.ListByTeam)
		protected.POST("/teams/:id/payments", paymentHandler.Record)

		protected.GET("/memberships/:membershipId/fees", feeHandler.ListByMembership)

		protected.GET("/fees/:id", feeHandler.GetByID)
		protected.PATCH("/fees/:id/status", feeHandler.UpdateStatus)
		protected.GET("/fees/:id/payment", paymentHandler.GetByObligationID)

		protected.GET("/payments/:id", paymentHandler.GetByID)
		protected.POST("/payments/:id/reverse", paymentHandler.Reverse)
	}

	slog.Info("server starting", "port", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
