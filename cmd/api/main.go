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
	"github.com/mbalmaceda/sports-hub-backend/internal/firebase"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/notification"
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
	competitionRepo := postgres.NewCompetitionRepository(pool)
	friendlyRepo := postgres.NewFriendlyRepository(pool)
	matchRepo := postgres.NewMatchRepository(pool)
	chargeRepo := postgres.NewChargeRepository(pool)
	fundsRepo := postgres.NewFundsRepository(pool)
	onboardingRepo := postgres.NewOnboardingRepository(pool)

	// Notifier. Los tokens de push viven en la tabla de usuarios, así que el
	// repositorio de usuarios es el que sabe a qué dispositivos escribir.
	notifications := notification.NewService(expo.New(), userRepo, slog.Default())

	// Firebase es opcional: sin credencial el backend arranca igual y solo queda
	// fuera lo que depende de Firestore.
	firebaseAuth, err := firebase.New(context.Background(), cfg.FirebaseServiceAccount)
	if err != nil {
		slog.Error("firebase error", "error", err)
		os.Exit(1)
	}
	slog.Info("firebase", "enabled", firebaseAuth.Enabled())

	// Handlers
	authHandler := handler.NewAuthHandler(userRepo, tokenRepo, cfg.JWTSecret)
	userHandler := handler.NewUserHandler(userRepo)
	firebaseHandler := handler.NewFirebaseHandler(firebaseAuth)
	teamHandler := handler.NewTeamHandler(teamRepo, rosterRepo, firebaseAuth)
	rosterHandler := handler.NewRosterHandler(rosterRepo, firebaseAuth)
	feeHandler := handler.NewFeeHandler(feeRepo, rosterRepo, teamRepo)
	paymentHandler := handler.NewPaymentHandler(paymentRepo, feeRepo)
	competitionHandler := handler.NewCompetitionHandler(competitionRepo, rosterRepo)
	friendlyHandler := handler.NewFriendlyHandler(friendlyRepo, competitionRepo, matchRepo, rosterRepo)
	matchHandler := handler.NewMatchHandler(matchRepo, rosterRepo, notifications)
	chargeHandler := handler.NewChargeHandler(chargeRepo, competitionRepo, matchRepo, rosterRepo, fundsRepo, notifications)
	onboardingHandler := handler.NewOnboardingHandler(onboardingRepo, teamRepo, rosterRepo, notifications)

	// Router
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	if cfg.CORSEnabled {
		r.Use(cors.New(cors.Config{
			AllowOrigins:     cfg.CORSAllowedOrigins,
			AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
			AllowCredentials: false,
		}))
		slog.Info("cors enabled", "allowed_origins", cfg.CORSAllowedOrigins)
	} else {
		slog.Info("cors disabled")
	}

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
		protected.DELETE("/users/me", userHandler.DeleteAccount)
		protected.PUT("/users/me/push-token", userHandler.RegisterPushToken)

		// Cambia la sesión de ZPORTS por una de Firebase, para que la app pueda
		// leer Firestore en vivo sin dejar de ser este backend quien autentica.
		protected.POST("/auth/firebase-token", firebaseHandler.IssueToken)

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

		// ── Competencias ──
		protected.GET("/teams/:id/competitions", competitionHandler.ListByTeam)
		protected.POST("/teams/:id/competitions", competitionHandler.Create)
		protected.GET("/teams/:id/competition-invitations", competitionHandler.ListInvitations)

		protected.GET("/competitions/:competitionId", competitionHandler.GetByID)
		protected.GET("/competitions/:competitionId/entries", competitionHandler.ListEntries)
		protected.POST("/competitions/:competitionId/invitations", competitionHandler.Invite)
		protected.POST("/competition-invitations/:invitationId/respond", competitionHandler.RespondToInvitation)

		// ── Amistosos ──
		protected.GET("/teams/:id/friendlies", friendlyHandler.ListByTeam)
		protected.POST("/teams/:id/friendlies", friendlyHandler.Create)
		protected.GET("/friendlies/:challengeId", friendlyHandler.GetByID)
		protected.GET("/friendlies/:challengeId/proposals", friendlyHandler.ListProposals)
		protected.POST("/friendlies/:challengeId/counter", friendlyHandler.Counter)
		protected.POST("/friendlies/:challengeId/accept", friendlyHandler.Accept)
		protected.POST("/friendlies/:challengeId/decline", friendlyHandler.Decline)

		// ── Partidos y convocatorias ──
		protected.GET("/teams/:id/matches", matchHandler.ListByTeam)
		protected.GET("/teams/:id/match-conflicts", matchHandler.ScheduleConflicts)
		protected.GET("/competitions/:competitionId/matches", matchHandler.ListByCompetition)
		protected.GET("/matches/:matchId", matchHandler.GetByID)
		protected.GET("/matches/:matchId/callups", matchHandler.ListCallups)
		protected.POST("/matches/:matchId/callups", matchHandler.CallUp)
		protected.POST("/matches/:matchId/callups/respond", matchHandler.RespondToCallup)
		protected.GET("/memberships/:membershipId/callups", matchHandler.ListCallupsByMembership)

		// ── Cobros ──
		protected.POST("/teams/:id/charges", chargeHandler.Split)
		protected.GET("/teams/:id/funds", chargeHandler.Funds)
		protected.GET("/teams/:id/bank-account", teamHandler.GetBankAccount)
		protected.PUT("/teams/:id/bank-account", teamHandler.SaveBankAccount)
		protected.GET("/competitions/:competitionId/charges", chargeHandler.ListByCompetition)
		protected.GET("/memberships/:membershipId/charges", chargeHandler.ListByMembership)
		protected.POST("/charges/:chargeId/receipt", chargeHandler.SubmitReceipt)
		protected.POST("/charges/:chargeId/confirm", chargeHandler.Confirm)
		protected.POST("/charges/:chargeId/reject", chargeHandler.RejectReceipt)

		// ── Incorporación ──
		protected.GET("/people/lookup", onboardingHandler.FindPerson)
		protected.GET("/teams/search", onboardingHandler.SearchTeams)
		protected.GET("/me/team-invitations", onboardingHandler.ListMyInvitations)
		protected.GET("/teams/:id/invitations", onboardingHandler.ListTeamInvitations)
		protected.POST("/teams/:id/invitations", onboardingHandler.InvitePerson)
		protected.POST("/team-invitations/:invitationId/respond", onboardingHandler.RespondToInvitation)
		protected.GET("/teams/:id/join-requests", onboardingHandler.ListJoinRequests)
		protected.POST("/teams/:id/join-requests", onboardingHandler.RequestToJoin)
		protected.POST("/join-requests/:requestId/respond", onboardingHandler.RespondToJoinRequest)
	}

	slog.Info("server starting", "port", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
