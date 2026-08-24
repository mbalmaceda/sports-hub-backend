package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/config"
	"github.com/mbalmaceda/sports-hub-backend/internal/db"
	"github.com/mbalmaceda/sports-hub-backend/internal/firebase"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/jobs"
	"github.com/mbalmaceda/sports-hub-backend/internal/middleware"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify"
	"github.com/mbalmaceda/sports-hub-backend/internal/notify/expo"
	"github.com/mbalmaceda/sports-hub-backend/internal/repository/postgres"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	config.LoadDotEnv()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}

	// Contexto de vida del proceso: se cancela con SIGINT/SIGTERM, que es lo que
	// manda Fly al apagar una máquina por inactividad o al desplegar.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	expenseRepo := postgres.NewExpenseRepository(pool)
	onboardingRepo := postgres.NewOnboardingRepository(pool)
	guestInviteRepo := postgres.NewGuestInviteRepository(pool)
	settlementRepo := postgres.NewSettlementRepository(pool)
	notificationRepo := postgres.NewNotificationRepository(pool)
	pushTokenRepo := postgres.NewPushTokenRepository(pool)

	// Emisión de novedades: escribe el registro y manda el push, en ese orden y
	// en la misma llamada. Son el mismo hecho contado dos veces —una que queda y
	// una que interrumpe— y separarlos deja que la lista y la bandeja discrepen.
	notifications := notify.NewService(expo.New(), notificationRepo, pushTokenRepo, slog.Default())

	// Firebase es opcional: sin credencial el backend arranca igual y solo queda
	// fuera lo que depende de Firestore.
	firebaseAuth, err := firebase.New(context.Background(), cfg.FirebaseServiceAccount)
	if err != nil {
		slog.Error("firebase error", "error", err)
		os.Exit(1)
	}
	defer firebaseAuth.Close()
	slog.Info("firebase", "enabled", firebaseAuth.Enabled())

	// El firmante guarda el secreto actual y el anterior, para poder rotar
	// JWT_SECRET sin cortar las sesiones abiertas.
	signer := auth.NewSigner(cfg.JWTSecret, cfg.JWTSecretPrevious)

	// Limpieza periódica de refresh tokens vencidos.
	auth.StartTokenReaper(ctx, tokenRepo, slog.Default())

	/*
		Lo que pasa porque pasó el tiempo: la barrida de lo vencido y los tres
		avisos que dispara el reloj.

		La barrida vivía adentro de cada GET, así que nada expiraba si nadie
		abría la app; los avisos directamente no existían. Que el scheduler no
		tenga memoria entre reinicios no es problema: lo que garantiza que un
		aviso salga una sola vez es `dedupe_key` en Postgres, no el horario.
	*/
	stopJobs := jobs.Start(ctx, jobs.Deps{
		Friendlies:    friendlyRepo,
		Competitions:  competitionRepo,
		Matches:       matchRepo,
		Fees:          feeRepo,
		Teams:         teamRepo,
		Memberships:   rosterRepo,
		Notifications: notifications,
	}, slog.Default())
	defer stopJobs()

	// Limitadores. Están en memoria y son exactos mientras Fly corra una sola
	// máquina; con varias, el límite efectivo se multiplica por la cantidad.
	loginByIP := middleware.New(10, time.Minute)
	loginByAccount := middleware.New(5, 15*time.Minute)
	registerByIP := middleware.New(5, time.Hour)
	refreshByIP := middleware.New(30, time.Minute)
	// La búsqueda de personas es por RUT: iterarla devuelve nombres, así que es
	// una superficie de enumeración de datos personales y no solo un buscador.
	lookupByUser := middleware.New(30, time.Minute)
	// La vista previa de una invitación es el único GET público de la app: se
	// entra con un token en la URL y sin sesión. Adivinar uno es imposible
	// (256 bits), pero sin límite queda un endpoint anónimo golpeable de a
	// millones, y cada intento pega en la base.
	inviteByIP := middleware.New(60, time.Minute)
	defer func() {
		for _, l := range []*middleware.Limiter{
			loginByIP, loginByAccount, registerByIP, refreshByIP, lookupByUser, inviteByIP,
		} {
			l.Stop()
		}
	}()

	// Handlers
	//
	// Sin GOOGLE_CLIENT_IDS el verificador es nil y el endpoint de Google
	// contesta 503: la app puede preguntar y enterarse, en vez de mandar tokens
	// contra algo que no está configurado.
	googleVerifier := auth.NewGoogleVerifier(cfg.GoogleClientIDs)
	authHandler := handler.NewAuthHandler(userRepo, tokenRepo, signer, loginByAccount, googleVerifier, slog.Default())
	userHandler := handler.NewUserHandler(userRepo)
	firebaseHandler := handler.NewFirebaseHandler(firebaseAuth)
	teamHandler := handler.NewTeamHandler(teamRepo, rosterRepo, firebaseAuth)
	rosterHandler := handler.NewRosterHandler(rosterRepo, firebaseAuth)
	feeHandler := handler.NewFeeHandler(feeRepo, rosterRepo, teamRepo)
	paymentHandler := handler.NewPaymentHandler(paymentRepo, feeRepo)
	competitionHandler := handler.NewCompetitionHandler(
		competitionRepo, rosterRepo, matchRepo, teamRepo,
		// Todo lo que hay que deshacer al cancelar un partido. Ver `Cancel`.
		chargeRepo, settlementRepo, fundsRepo, guestInviteRepo,
		notifications)
	friendlyHandler := handler.NewFriendlyHandler(
		friendlyRepo, competitionRepo, matchRepo, rosterRepo, settlementRepo, teamRepo, notifications)
	matchHandler := handler.NewMatchHandler(matchRepo, rosterRepo, competitionRepo, chargeRepo, notifications, firebaseAuth)
	chargeHandler := handler.NewChargeHandler(chargeRepo, competitionRepo, matchRepo, rosterRepo, fundsRepo, notifications)
	expenseHandler := handler.NewExpenseHandler(expenseRepo, competitionRepo, rosterRepo)
	settlementHandler := handler.NewSettlementHandler(
		settlementRepo, competitionRepo, matchRepo, rosterRepo, teamRepo)
	onboardingHandler := handler.NewOnboardingHandler(
		onboardingRepo, teamRepo, rosterRepo, userRepo, notifications, firebaseAuth)
	guestHandler := handler.NewGuestHandler(
		guestInviteRepo, matchRepo, rosterRepo, competitionRepo, chargeRepo, teamRepo, userRepo,
		firebaseAuth, notifications, cfg)
	appLinksHandler := handler.NewAppLinksHandler(guestInviteRepo, cfg)
	notificationHandler := handler.NewNotificationHandler(notificationRepo, pushTokenRepo, rosterRepo, notifications)

	// Router
	r := gin.New()

	// Detrás del proxy de Fly y de ningún otro: de esto depende que los
	// limitadores de abajo no se puedan saltar falsificando X-Forwarded-For.
	if err := middleware.ConfigureTrustedProxies(r); err != nil {
		slog.Error("could not set trusted proxies", "error", err)
		os.Exit(1)
	}

	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(slog.Default()))
	r.Use(gin.Recovery())
	r.Use(middleware.SecureHeaders())
	r.Use(middleware.BodyLimit(cfg.MaxBodyBytes))
	if cfg.CORSEnabled {
		r.Use(cors.New(cors.Config{
			AllowOrigins: cfg.CORSAllowedOrigins,
			// Los preview deploys de Vercel estrenan subdominio en cada commit,
			// así que no se pueden enumerar. Solo se consulta cuando el origen
			// no estaba en la lista exacta.
			AllowOriginFunc: cfg.AllowOrigin,
			AllowMethods:    []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders:    []string{"Origin", "Content-Type", "Authorization"},
			// Los tokens viajan en Authorization, no en cookies: el navegador no
			// tiene credenciales que mandar solo, y habilitarlas obligaría a
			// dejar de aceptar orígenes por patrón.
			AllowCredentials: false,
			ExposeHeaders:    []string{"X-Request-Id"},
			// Sin esto hay un preflight OPTIONS por cada request.
			MaxAge: 12 * time.Hour,
		}))
		slog.Info("cors enabled",
			"allowed_origins", cfg.CORSAllowedOrigins,
			"allowed_origin_regex", cfg.CORSAllowedOriginRegex)
	} else {
		slog.Info("cors disabled")
	}

	r.GET("/health", handler.Health)

	r.POST("/auth/register", registerByIP.ByIP(), authHandler.Register)
	// El límite por cuenta va adentro del handler, donde ya está parseado el
	// email: acá solo se puede limitar por IP.
	r.POST("/auth/login", loginByIP.ByIP(), authHandler.Login)
	// La otra puerta de entrada, con el mismo limitador por IP. No lleva el de
	// por cuenta: acá no hay contraseña que adivinar, la credencial la firma
	// Google.
	r.POST("/auth/google", loginByIP.ByIP(), authHandler.GoogleSignIn)
	r.POST("/auth/refresh", refreshByIP.ByIP(), authHandler.Refresh)
	r.POST("/auth/logout", authHandler.Logout)

	// Vista previa de una invitación a un partido. Va afuera del grupo
	// protegido a propósito: el destinatario todavía no tiene cuenta, y pedirle
	// sesión para ver a qué lo invitan sería pedirle que se registre a ciegas.
	// Lo que devuelve está recortado para que sea seguro publicarlo.
	r.GET("/invites/:token", inviteByIP.ByIP(), guestHandler.GetInvite)

	// El enlace que se comparte por WhatsApp apunta acá: una página HTML que
	// abre la app si está instalada y ofrece descargarla si no. Los dos
	// .well-known son lo que hace que el sistema operativo se salte la página y
	// abra la app directo. Ver internal/handler/applinks_handler.go.
	r.GET("/i/:token", inviteByIP.ByIP(), appLinksHandler.InvitePage)
	r.GET("/.well-known/assetlinks.json", appLinksHandler.AssetLinks)
	r.GET("/.well-known/apple-app-site-association", appLinksHandler.AppleAppSiteAssociation)

	// Rutas protegidas — requieren JWT válido
	protected := r.Group("/")
	protected.Use(auth.Middleware(signer))
	{
		protected.POST("/auth/logout-all", authHandler.LogoutAll)

		protected.GET("/users/me", userHandler.Me)
		protected.PATCH("/users/me", userHandler.UpdateProfile)
		protected.DELETE("/users/me", userHandler.DeleteAccount)
		// El registro del dispositivo cuelga del handler de notificaciones desde
		// que los tokens son tabla propia: dejó de ser un dato del perfil. La
		// ruta no cambió —la app la llama en cada arranque—.
		protected.PUT("/users/me/push-token", notificationHandler.RegisterPushToken)

		// ── Novedades ──
		// Personales, no del equipo: quien juega en dos clubes las ve juntas, y
		// quien todavía no tiene equipo —el recién invitado— también las tiene.
		protected.GET("/me/notifications", notificationHandler.List)
		protected.POST("/me/notifications/read-all", notificationHandler.MarkAllRead)
		protected.POST("/notifications/:notificationId/read", notificationHandler.MarkRead)
		// El aviso del manager al plantel: la única notificación que nace de un
		// toque en la app, y la única sin pantalla a la que llevar.
		protected.POST("/teams/:id/announcements", notificationHandler.Announce)

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
		// Sumar al plantel a un invitado que ya jugó: el "parche" que se queda.
		protected.POST("/teams/:id/roster/:membershipId/promote", rosterHandler.PromoteGuest)

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
		protected.POST("/competitions/:competitionId/cancel", competitionHandler.Cancel)
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
		// Amistoso sin rival: el equipo juega contra sí mismo. Cuelga del mismo
		// handler porque es un amistoso, solo que no hay a quién esperar.
		protected.POST("/teams/:id/internal-matches", friendlyHandler.CreateInternal)

		// ── Partidos y convocatorias ──
		protected.GET("/teams/:id/matches", matchHandler.ListByTeam)
		protected.GET("/teams/:id/match-conflicts", matchHandler.ScheduleConflicts)
		protected.GET("/competitions/:competitionId/matches", matchHandler.ListByCompetition)
		protected.GET("/matches/:matchId", matchHandler.GetByID)
		protected.GET("/matches/:matchId/callups", matchHandler.ListCallups)
		protected.POST("/matches/:matchId/callups", matchHandler.CallUp)
		protected.POST("/matches/:matchId/callups/respond", matchHandler.RespondToCallup)
		// El marcador cierra el partido. PUT y no POST porque es un upsert:
		// mandarlo de nuevo corrige el resultado, no agrega otro.
		protected.PUT("/matches/:matchId/result", matchHandler.SaveResult)
		protected.GET("/memberships/:membershipId/callups", matchHandler.ListCallupsByMembership)

		// ── Invitados de un partido ("parches") ──
		// Gente de afuera que completa una convocatoria. El enlace se canjea
		// con una cuenta ya creada: el alta sigue siendo POST /auth/register.
		protected.GET("/matches/:matchId/guest-invites", guestHandler.ListInvites)
		protected.POST("/matches/:matchId/guest-invites", guestHandler.CreateInvite)
		protected.POST("/invites/:token/accept", guestHandler.Accept)
		protected.DELETE("/guest-invites/:inviteId", guestHandler.RevokeInvite)

		// ── Cobros ──
		protected.POST("/teams/:id/charges", chargeHandler.Split)
		protected.GET("/teams/:id/charges", chargeHandler.ListByTeamAndPeriod)
		protected.GET("/teams/:id/funds", chargeHandler.Funds)
		protected.GET("/teams/:id/bank-account", teamHandler.GetBankAccount)
		protected.PUT("/teams/:id/bank-account", teamHandler.SaveBankAccount)
		protected.GET("/competitions/:competitionId/charges", chargeHandler.ListByCompetition)
		protected.GET("/memberships/:membershipId/charges", chargeHandler.ListByMembership)
		protected.POST("/charges/:chargeId/receipt", chargeHandler.SubmitReceipt)
		protected.POST("/charges/:chargeId/confirm", chargeHandler.Confirm)
		protected.POST("/charges/:chargeId/reject", chargeHandler.RejectReceipt)
		// Cerrar un pendiente sin plata por la app: cobrado en efectivo, o
		// incobrable. Es la única salida que tiene un cargo que nadie va a pagar.
		protected.POST("/charges/:chargeId/waive", chargeHandler.Waive)

		// ── Lo que un equipo le debe a otro por la cancha ──
		// La reserva y la paga el organizador; el retado le transfiere su
		// mitad. Es una sola transferencia entre managers, no catorce.
		protected.GET("/teams/:id/settlements", settlementHandler.ListByTeam)
		protected.GET("/competitions/:competitionId/settlement", settlementHandler.GetByCompetition)
		protected.GET("/settlements/:settlementId/bank-account", settlementHandler.GetPayeeBankAccount)
		protected.POST("/settlements/:settlementId/pay", settlementHandler.Pay)

		// ── Gastos ──
		protected.GET("/teams/:id/expenses", expenseHandler.ListByTeamAndPeriod)
		protected.POST("/teams/:id/expenses", expenseHandler.Create)
		protected.GET("/competitions/:competitionId/expenses", expenseHandler.ListByCompetition)
		protected.DELETE("/expenses/:expenseId", expenseHandler.Delete)

		// ── Incorporación ──
		protected.GET("/people/lookup", lookupByUser.ByUser(), onboardingHandler.FindPerson)
		protected.GET("/teams/search", lookupByUser.ByUser(), onboardingHandler.SearchTeams)
		protected.GET("/me/team-invitations", onboardingHandler.ListMyInvitations)
		protected.GET("/teams/:id/invitations", onboardingHandler.ListTeamInvitations)
		protected.POST("/teams/:id/invitations", onboardingHandler.InvitePerson)
		protected.POST("/team-invitations/:invitationId/respond", onboardingHandler.RespondToInvitation)
		protected.GET("/teams/:id/join-requests", onboardingHandler.ListJoinRequests)
		protected.POST("/teams/:id/join-requests", onboardingHandler.RequestToJoin)
		protected.POST("/join-requests/:requestId/respond", onboardingHandler.RespondToJoinRequest)
	}

	// Servidor explícito en vez de r.Run(): lo que importa son los timeouts, que
	// el default de Go deja en cero. Sin ReadHeaderTimeout, una conexión que
	// manda cabeceras de a un byte se queda abierta para siempre, y en una
	// máquina de 256 MB no hacen falta muchas para dejarla sin memoria.
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: config.ReadHeaderTimeout,
		ReadTimeout:       config.ReadTimeout,
		WriteTimeout:      config.WriteTimeout,
		IdleTimeout:       config.IdleTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		slog.Error("server error", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		// Fly manda SIGTERM al desplegar y al suspender la máquina por falta de
		// tráfico (auto_stop_machines). Hasta acá eso cortaba los requests en
		// vuelo; ahora se les da tiempo de terminar.
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown error", "error", err)
		}
		slog.Info("stopped")
	}
}
