package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/mbalmaceda/sports-hub-backend/internal/domain/venue"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

func adminRequest(method, target, body string) (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		c.Request.Header.Set("Content-Type", "application/json")
	}
	return w, c
}

func TestAdminListVenues_FiltersByStatus(t *testing.T) {
	vr := &testutil.MockVenueRepo{}
	vr.On("List", mock.Anything, venue.Filter{Status: venue.StatusRejected}).
		Return([]*venue.AdminVenue{{Venue: venue.Venue{ID: "v-1"}}}, nil)

	w, c := adminRequest(http.MethodGet, "/admin/venues?status=rejected", "")
	handler.NewVenueHandler(vr).AdminList(c)

	assert.Equal(t, http.StatusOK, w.Code)
	vr.AssertExpectations(t)
}

// `verified` es el filtro de antes de que hubiera rechazadas: false son las
// pendientes, no "todo lo que no está verificado" (eso incluiría rechazadas).
func TestAdminListVenues_LegacyVerifiedFalseMeansPending(t *testing.T) {
	vr := &testutil.MockVenueRepo{}
	vr.On("List", mock.Anything, venue.Filter{Status: venue.StatusPending}).
		Return([]*venue.AdminVenue{}, nil)

	w, c := adminRequest(http.MethodGet, "/admin/venues?verified=false", "")
	handler.NewVenueHandler(vr).AdminList(c)

	assert.Equal(t, http.StatusOK, w.Code)
	vr.AssertExpectations(t)
}

func TestAdminListVenues_UnknownStatusIsRejected(t *testing.T) {
	vr := &testutil.MockVenueRepo{}

	w, c := adminRequest(http.MethodGet, "/admin/venues?status=borrada", "")
	handler.NewVenueHandler(vr).AdminList(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	vr.AssertNotCalled(t, "List", mock.Anything, mock.Anything)
}

func TestAdminUpdateVenue_Reject(t *testing.T) {
	vr := &testutil.MockVenueRepo{}
	current := &venue.Venue{ID: "v-1", Name: "Cancha", Lat: -33.5, Lng: -70.7}
	vr.On("FindByID", mock.Anything, "v-1").Return(current, nil)
	vr.On("Update", mock.Anything, "v-1", mock.MatchedBy(func(u venue.Update) bool {
		return u.Rejected != nil && *u.Rejected && u.Verified == nil
	})).Return(current, nil)

	w, c := adminRequest(http.MethodPatch, "/admin/venues/v-1", `{"rejected":true}`)
	c.Params = gin.Params{{Key: "venueId", Value: "v-1"}}
	handler.NewVenueHandler(vr).AdminUpdate(c)

	assert.Equal(t, http.StatusOK, w.Code)
	vr.AssertExpectations(t)
}

// Verificada y rechazada a la vez no es un estado: se rechaza antes de tocar nada.
func TestAdminUpdateVenue_VerifiedAndRejectedIsContradictory(t *testing.T) {
	vr := &testutil.MockVenueRepo{}

	w, c := adminRequest(http.MethodPatch, "/admin/venues/v-1", `{"verified":true,"rejected":true}`)
	c.Params = gin.Params{{Key: "venueId", Value: "v-1"}}
	handler.NewVenueHandler(vr).AdminUpdate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	vr.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminNearby_MeasuresFromTheVenue(t *testing.T) {
	vr := &testutil.MockVenueRepo{}
	vr.On("FindByID", mock.Anything, "v-1").Return(&venue.Venue{ID: "v-1"}, nil)
	vr.On("Nearby", mock.Anything, "v-1", venue.NearbyRadiusKm).
		Return([]*venue.Venue{{ID: "v-2", Name: "Club Dominica Sport"}}, nil)

	w, c := adminRequest(http.MethodGet, "/admin/venues/v-1/nearby", "")
	c.Params = gin.Params{{Key: "venueId", Value: "v-1"}}
	handler.NewVenueHandler(vr).AdminNearby(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Club Dominica Sport")
}

func TestAdminNearby_UnknownVenueIs404(t *testing.T) {
	vr := &testutil.MockVenueRepo{}
	vr.On("FindByID", mock.Anything, "v-x").Return(nil, venue.ErrNotFound)

	w, c := adminRequest(http.MethodGet, "/admin/venues/v-x/nearby", "")
	c.Params = gin.Params{{Key: "venueId", Value: "v-x"}}
	handler.NewVenueHandler(vr).AdminNearby(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	vr.AssertNotCalled(t, "Nearby", mock.Anything, mock.Anything, mock.Anything)
}
