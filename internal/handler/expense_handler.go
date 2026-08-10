package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/competition"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/expense"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/membership"
)

type ExpenseHandler struct {
	expenses     expense.Repository
	competitions competition.Repository
	memberships  membership.Repository
	authz        teamAuthorizer
}

func NewExpenseHandler(
	expenses expense.Repository,
	competitions competition.Repository,
	memberships membership.Repository,
) *ExpenseHandler {
	return &ExpenseHandler{
		expenses:     expenses,
		competitions: competitions,
		memberships:  memberships,
		authz:        teamAuthorizer{memberships: memberships},
	}
}

type createExpenseRequest struct {
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Category    string `json:"category"`
	Description string `json:"description"`
	// Origen opcional: de qué partido salió el gasto. Van los dos o ninguno.
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	// "2026-08-09". Vacío significa hoy.
	ExpenseDate string `json:"expense_date"`
}

// ListByTeamAndPeriod GET /teams/:id/expenses?year=2026&month=8
//
// Lo ve cualquier miembro: en un equipo amateur la plata que sale es
// información del grupo, no del que la anotó. Anotarla sí pide rol.
func (h *ExpenseHandler) ListByTeamAndPeriod(c *gin.Context) {
	teamID := c.Param("id")
	if _, err := h.authz.requireMember(c, teamID); abortAuthz(c, err) {
		return
	}

	year, month, ok := periodFromQuery(c)
	if !ok {
		return
	}

	expenses, err := h.expenses.ListByTeamAndPeriod(c.Request.Context(), teamID, year, month)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list expenses"})
		return
	}
	if expenses == nil {
		expenses = []*expense.Expense{}
	}
	c.JSON(http.StatusOK, expenses)
}

// ListByCompetition GET /competitions/:competitionId/expenses
//
// Los gastos que cuelgan de ese partido, para su balance contra lo recaudado.
func (h *ExpenseHandler) ListByCompetition(c *gin.Context) {
	competitionID := c.Param("competitionId")

	comp, err := h.competitions.FindByID(c.Request.Context(), competitionID)
	if errors.Is(err, competition.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "competition not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if _, err := h.authz.requireMember(c, comp.OrganizerTeamID); abortAuthz(c, err) {
		return
	}

	expenses, err := h.expenses.ListBySource(c.Request.Context(), expense.Source{
		Type: expense.SourceMatchCost,
		ID:   competitionID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list expenses"})
		return
	}
	if expenses == nil {
		expenses = []*expense.Expense{}
	}
	c.JSON(http.StatusOK, expenses)
}

// Create POST /teams/:id/expenses
func (h *ExpenseHandler) Create(c *gin.Context) {
	teamID := c.Param("id")

	// Mismo par de roles que el reparto de cobros: quien mueve la plata del
	// equipo la mueve entera, no solo la que entra.
	if _, err := h.authz.requireRole(
		c, teamID, membership.RoleManager, membership.RoleTreasurer,
	); abortAuthz(c, err) {
		return
	}

	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req createExpenseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "amount must be greater than zero"})
		return
	}
	if req.Category == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "category is required"})
		return
	}
	if req.Currency == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "currency is required"})
		return
	}

	source, err := h.resolveSource(c, teamID, req)
	if err != nil {
		return // resolveSource ya respondió
	}

	expenseDate, ok := parseExpenseDate(c, req.ExpenseDate)
	if !ok {
		return
	}

	created, err := h.expenses.Create(c.Request.Context(), expense.CreateInput{
		TeamID:      teamID,
		RecordedBy:  userID,
		Amount:      req.Amount,
		Currency:    req.Currency,
		Category:    req.Category,
		Description: req.Description,
		Source:      source,
		ExpenseDate: expenseDate,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create expense"})
		return
	}

	c.JSON(http.StatusCreated, created)
}

// Delete DELETE /expenses/:expenseId
//
// Un gasto mal cargado deforma el balance del mes y del partido, y no hay
// edición: se borra y se vuelve a anotar.
func (h *ExpenseHandler) Delete(c *gin.Context) {
	existing, err := h.expenses.GetByID(c.Request.Context(), c.Param("expenseId"))
	if errors.Is(err, expense.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "expense not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if _, err := h.authz.requireRole(
		c, existing.TeamID, membership.RoleManager, membership.RoleTreasurer,
	); abortAuthz(c, err) {
		return
	}

	if err := h.expenses.Delete(c.Request.Context(), existing.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete expense"})
		return
	}
	c.Status(http.StatusNoContent)
}

// resolveSource valida el origen del gasto. Devuelve nil sin error cuando el
// gasto no cuelga de ningún partido, que es un caso válido. Si algo está mal ya
// respondió el error, y el llamador solo tiene que volverse.
func (h *ExpenseHandler) resolveSource(
	c *gin.Context, teamID string, req createExpenseRequest,
) (*expense.Source, error) {
	if req.SourceType == "" && req.SourceID == "" {
		return nil, nil
	}
	if req.SourceType == "" || req.SourceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": expense.ErrInvalidSource.Error()})
		return nil, expense.ErrInvalidSource
	}
	if expense.SourceType(req.SourceType) != expense.SourceMatchCost {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown source type"})
		return nil, expense.ErrInvalidSource
	}

	// La competencia tiene que existir y ser de este equipo. Sin esto, un
	// manager podría colgarle gastos al partido de otro club y ensuciarle el
	// balance desde afuera.
	comp, err := h.competitions.FindByID(c.Request.Context(), req.SourceID)
	if errors.Is(err, competition.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "competition not found"})
		return nil, err
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, err
	}
	if comp.OrganizerTeamID != teamID {
		c.JSON(http.StatusForbidden, gin.H{"error": "competition belongs to another team"})
		return nil, expense.ErrInvalidSource
	}

	return &expense.Source{Type: expense.SourceMatchCost, ID: req.SourceID}, nil
}

// parseExpenseDate lee la fecha del gasto. Vacía es hoy: el caso corriente es
// anotar algo que se acaba de pagar.
func parseExpenseDate(c *gin.Context, raw string) (time.Time, bool) {
	if raw == "" {
		now := time.Now().UTC()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), true
	}

	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expense_date must be YYYY-MM-DD"})
		return time.Time{}, false
	}
	return parsed, true
}

// periodFromQuery lee ?year=&month=. Sin parámetros usa el mes en curso, que es
// lo que la app pide el 90% de las veces.
func periodFromQuery(c *gin.Context) (int, int, bool) {
	now := time.Now().UTC()
	year, month := now.Year(), int(now.Month())

	if raw := c.Query("year"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 2000 || parsed > 2200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid year"})
			return 0, 0, false
		}
		year = parsed
	}
	if raw := c.Query("month"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 12 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid month"})
			return 0, 0, false
		}
		month = parsed
	}
	return year, month, true
}
