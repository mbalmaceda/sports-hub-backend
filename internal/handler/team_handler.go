package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/team"
	"github.com/mbalmaceda/sports-hub-backend/internal/firebase"
)

type TeamHandler struct {
	repo        team.Repository
	memberships membership.Repository
	firebase    *firebase.Firebase
	authz       teamAuthorizer
}

func NewTeamHandler(repo team.Repository, memberships membership.Repository, fb *firebase.Firebase) *TeamHandler {
	return &TeamHandler{
		repo:        repo,
		memberships: memberships,
		firebase:    fb,
		authz:       teamAuthorizer{memberships: memberships},
	}
}

// Create POST /teams
// El creador queda asignado automáticamente como manager del equipo.
func (h *TeamHandler) Create(c *gin.Context) {
	claims, ok := auth.ClaimsFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Name     string `json:"name"       binding:"required"`
		SportID  string `json:"sport_id"   binding:"required"`
		Category string `json:"category"   binding:"required"`
		// Opcionales: un equipo puede nacer sin ciudad ni descripción, y el alta
		// no las pide como requisito para no alargar el primer paso.
		City        string `json:"city"`
		Description string `json:"description"`
		ClubID      string `json:"club_id"`
		LogoURL     string `json:"logo_url"`
		FeeAmount   int64  `json:"fee_amount"`
		FeeDueDay   int    `json:"fee_due_day" binding:"min=1,max=31"`
		Currency    string `json:"currency"   binding:"required"`
		// Puntero para distinguir "no lo mandó" de "dijo que no juega": un
		// cliente viejo que omita el campo tiene que seguir creando un manager
		// que no ocupa lugar en la plantilla.
		PlaysAsPlayer *bool `json:"plays_as_player"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	t := &team.Team{
		Name:        req.Name,
		SportID:     req.SportID,
		Category:    req.Category,
		City:        req.City,
		Description: req.Description,
		ClubID:      req.ClubID,
		LogoURL:     req.LogoURL,
		FeeAmount:   req.FeeAmount,
		FeeDueDay:   req.FeeDueDay,
		Currency:    req.Currency,
		IsActive:    true,
	}
	if err := h.repo.Create(c.Request.Context(), t); err != nil {
		if errors.Is(err, team.ErrNameTaken) {
			c.JSON(http.StatusConflict, gin.H{"error": "team name already taken"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create team"})
		return
	}

	playsAsPlayer := membership.DefaultPlaysAsPlayer(membership.RoleManager)
	if req.PlaysAsPlayer != nil {
		playsAsPlayer = *req.PlaysAsPlayer
	}

	m := &membership.Membership{
		UserID:        claims.UserID,
		TeamID:        t.ID,
		Role:          membership.RoleManager,
		PlaysAsPlayer: playsAsPlayer,
		Status:        membership.StatusActive,
	}
	if err := h.memberships.Create(c.Request.Context(), m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "team created but could not assign manager membership"})
		return
	}

	// Sin esto, quien acaba de crear el equipo no existe para las reglas de
	// Firestore y no podría leer ni su propio plantel.
	h.firebase.SyncMembershipAsync(firebase.Membership{
		TeamID: m.TeamID,
		UserID: m.UserID,
		Role:   string(m.Role),
		Status: string(m.Status),
	})

	c.JSON(http.StatusCreated, t)
}

func (h *TeamHandler) GetByID(c *gin.Context) {
	t, err := h.repo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

func (h *TeamHandler) List(c *gin.Context) {
	teams, err := h.repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list teams"})
		return
	}
	if teams == nil {
		teams = []*team.Team{}
	}
	c.JSON(http.StatusOK, teams)
}

func (h *TeamHandler) UpdateFeeConfig(c *gin.Context) {
	var req struct {
		FeeAmount int64 `json:"fee_amount" binding:"required"`
		FeeDueDay int   `json:"fee_due_day" binding:"required,min=1,max=31"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := h.repo.UpdateFeeConfig(c.Request.Context(), c.Param("id"), team.FeeConfig{
		FeeAmount: req.FeeAmount,
		FeeDueDay: req.FeeDueDay,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update fee config"})
		return
	}

	t, err := h.repo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "updated"})
		return
	}
	c.JSON(http.StatusOK, t)
}

// GetBankAccount GET /teams/:id/bank-account
//
// Cualquier miembro puede verla: es a donde tiene que transferir cuando le
// toca pagar algo. Un 404 acá es un caso normal —el equipo todavía no la
// cargó— y la app muestra el estado vacío en vez de un error.
func (h *TeamHandler) GetBankAccount(c *gin.Context) {
	teamID := c.Param("id")
	// Incluye a los invitados: es la cuenta a la que hay que transferir, y el
	// parche paga su cuota de cancha por la app como cualquiera. Esconderle los
	// datos bancarios sería cobrarle sin decirle dónde pagar.
	if _, err := h.authz.requireMembership(c, teamID); abortAuthz(c, err) {
		return
	}

	acc, err := h.repo.GetBankAccount(c.Request.Context(), teamID)
	if errors.Is(err, team.ErrBankAccountNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "team has no bank account on file"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, acc)
}

// SaveBankAccount PUT /teams/:id/bank-account
// La edita quien maneja la plata del equipo.
func (h *TeamHandler) SaveBankAccount(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireRole(c, teamID, membership.RoleManager, membership.RoleTreasurer); abortAuthz(c, err) {
		return
	}

	var req struct {
		BankName      string `json:"bank_name"      binding:"required"`
		AccountType   string `json:"account_type"   binding:"required"`
		AccountNumber string `json:"account_number" binding:"required"`
		HolderName    string `json:"holder_name"    binding:"required"`
		HolderTaxID   string `json:"holder_tax_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	acc := &team.BankAccount{
		TeamID:        teamID,
		BankName:      req.BankName,
		AccountType:   req.AccountType,
		AccountNumber: req.AccountNumber,
		HolderName:    req.HolderName,
		HolderTaxID:   req.HolderTaxID,
	}
	if err := h.repo.SaveBankAccount(c.Request.Context(), acc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save the bank account"})
		return
	}
	c.JSON(http.StatusOK, acc)
}
