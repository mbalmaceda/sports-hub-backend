package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/venue"
)

// venueSuggestions es cuántas canchas devuelve el autocompletado: las que
// caben en pantalla sobre el teclado.
const venueSuggestions = 8

/*
VenueHandler es el catálogo de canchas, con dos puertas.

La de la app (`/venues`) busca y sugiere: cualquiera con sesión puede buscar, y
quien no encuentra su cancha la sugiere con nombre y pin. Lo sugerido nace sin
verificar y sirve igual.

La del admin (`/admin/venues`) corrige, verifica y fusiona. Hoy la protege un
token de servicio (`middleware.AdminToken`); cuando exista el admin web va a
ser un usuario con permiso, y estos handlers no cambian.
*/
type VenueHandler struct {
	venues venue.Repository
}

func NewVenueHandler(venues venue.Repository) *VenueHandler {
	return &VenueHandler{venues: venues}
}

// Search GET /venues?q&lat&lng
//
// Sin `q` devuelve las más cercanas: lo primero que ve quien abre el campo es
// la lista de canchas alrededor, antes de escribir nada.
func (h *VenueHandler) Search(c *gin.Context) {
	var lat, lng *float64
	if la, lo, ok := parseCoordinate(c); ok {
		lat, lng = &la, &lo
	}

	items, err := h.venues.Search(c.Request.Context(), c.Query("q"), lat, lng, venueSuggestions)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not search venues"})
		return
	}
	if items == nil {
		items = []*venue.Venue{}
	}
	c.JSON(http.StatusOK, items)
}

type venueInput struct {
	Name string  `json:"name" binding:"required,max=120"`
	Lat  float64 `json:"lat"  binding:"required"`
	Lng  float64 `json:"lng"  binding:"required"`
}

// Suggest POST /venues
//
// 201 si la creó, 200 si ya existía una igual a menos de 300 m: para la app
// las dos son "acá está tu cancha".
func (h *VenueHandler) Suggest(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req venueInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	v := &venue.Venue{Name: req.Name, Lat: req.Lat, Lng: req.Lng, CreatedBy: &userID}
	if err := v.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	saved, created, err := h.venues.FindOrCreate(c.Request.Context(), v)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save the venue"})
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, saved)
}

// ── Admin ──

// AdminList GET /admin/venues?q&verified
//
// Las sin verificar van primero: son lo que hay que revisar.
func (h *VenueHandler) AdminList(c *gin.Context) {
	f := venue.Filter{Query: c.Query("q")}
	if raw := c.Query("verified"); raw != "" {
		verified, err := strconv.ParseBool(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "verified must be true or false"})
			return
		}
		f.Verified = &verified
	}

	items, err := h.venues.List(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list venues"})
		return
	}
	if items == nil {
		items = []*venue.Venue{}
	}
	c.JSON(http.StatusOK, items)
}

// AdminCreate POST /admin/venues
//
// Lo que carga el admin nace verificado: es el catálogo curado.
func (h *VenueHandler) AdminCreate(c *gin.Context) {
	var req venueInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	v := &venue.Venue{Name: req.Name, Lat: req.Lat, Lng: req.Lng, Verified: true}
	if err := v.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.venues.Create(c.Request.Context(), v); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create the venue"})
		return
	}
	c.JSON(http.StatusCreated, v)
}

// AdminUpdate PATCH /admin/venues/:venueId
func (h *VenueHandler) AdminUpdate(c *gin.Context) {
	var req venue.Update
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Se valida el resultado y no cada campo: mover solo la latitud tiene que
	// dejar una coordenada válida junto con la longitud que ya estaba.
	ctx := c.Request.Context()
	current, err := h.venues.FindByID(ctx, c.Param("venueId"))
	if errors.Is(err, venue.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	next := *current
	if req.Name != nil {
		next.Name = *req.Name
	}
	if req.Lat != nil {
		next.Lat = *req.Lat
	}
	if req.Lng != nil {
		next.Lng = *req.Lng
	}
	if err := next.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := h.venues.Update(ctx, current.ID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update the venue"})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// AdminMerge POST /admin/venues/:venueId/merge {into_id}
//
// Fusiona `:venueId` (el duplicado) en `into_id` (la buena) y borra el duplicado.
func (h *VenueHandler) AdminMerge(c *gin.Context) {
	var req struct {
		IntoID string `json:"into_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fromID := c.Param("venueId")
	if fromID == req.IntoID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot merge a venue into itself"})
		return
	}

	err := h.venues.Merge(c.Request.Context(), fromID, req.IntoID)
	if errors.Is(err, venue.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not merge venues"})
		return
	}
	c.Status(http.StatusNoContent)
}
