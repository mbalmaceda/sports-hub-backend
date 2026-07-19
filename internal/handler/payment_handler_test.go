package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	authjwt "github.com/mbalmaceda/sports-hub-backend/internal/auth"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/fee"
	"github.com/mbalmaceda/sports-hub-backend/internal/domain/payment"
	"github.com/mbalmaceda/sports-hub-backend/internal/handler"
	"github.com/mbalmaceda/sports-hub-backend/internal/testutil"
)

func contextWithClaims(c *gin.Context, userID string) {
	c.Set("claims", &authjwt.Claims{UserID: userID})
}

func TestRecordPayment_Success(t *testing.T) {
	paymentRepo := &testutil.MockPaymentRepo{}
	feeRepo := &testutil.MockFeeRepo{}
	h := handler.NewPaymentHandler(paymentRepo, feeRepo)

	paymentRepo.On("Create", mock.Anything, mock.AnythingOfType("*payment.Payment")).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	contextWithClaims(c, "recorder-user")
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/team-1/payments",
		strings.NewReader(`{"payer_id":"payer-1","amount":10000,"currency":"CLP","method":"cash"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Record(c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "payer-1", body["payer_id"])
	assert.Equal(t, "recorder-user", body["recorded_by"])
	assert.Equal(t, float64(10000), body["amount"])
}

func TestRecordPayment_WithObligation_MarksPaid(t *testing.T) {
	paymentRepo := &testutil.MockPaymentRepo{}
	feeRepo := &testutil.MockFeeRepo{}
	h := handler.NewPaymentHandler(paymentRepo, feeRepo)

	obligationID := "fee-1"
	paymentRepo.On("Create", mock.Anything, mock.AnythingOfType("*payment.Payment")).Return(nil)
	feeRepo.On("UpdateStatus", mock.Anything, obligationID, fee.StatusPaid, mock.AnythingOfType("*time.Time")).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	contextWithClaims(c, "recorder-user")
	body := `{"obligation_id":"fee-1","payer_id":"payer-1","amount":10000,"currency":"CLP","method":"transfer"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/team-1/payments", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Record(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	feeRepo.AssertCalled(t, "UpdateStatus", mock.Anything, obligationID, fee.StatusPaid, mock.AnythingOfType("*time.Time"))
}

func TestRecordPayment_WithoutObligation_DoesNotUpdateFee(t *testing.T) {
	paymentRepo := &testutil.MockPaymentRepo{}
	feeRepo := &testutil.MockFeeRepo{}
	h := handler.NewPaymentHandler(paymentRepo, feeRepo)

	paymentRepo.On("Create", mock.Anything, mock.AnythingOfType("*payment.Payment")).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "team-1"}}
	contextWithClaims(c, "recorder-user")
	c.Request = httptest.NewRequest(http.MethodPost, "/teams/team-1/payments",
		strings.NewReader(`{"payer_id":"payer-1","amount":5000,"currency":"CLP","method":"other"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Record(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	feeRepo.AssertNotCalled(t, "UpdateStatus")
}

func TestReversePayment_Success(t *testing.T) {
	paymentRepo := &testutil.MockPaymentRepo{}
	feeRepo := &testutil.MockFeeRepo{}
	h := handler.NewPaymentHandler(paymentRepo, feeRepo)

	p := &payment.Payment{
		ID:           "pay-1",
		ObligationID: "fee-1",
		IsReversed:   false,
	}

	paymentRepo.On("FindByID", mock.Anything, "pay-1").Return(p, nil)
	paymentRepo.On("Reverse", mock.Anything, "pay-1").Return(nil)
	feeRepo.On("UpdateStatus", mock.Anything, "fee-1", fee.StatusPending, (*time.Time)(nil)).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "pay-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/payments/pay-1/reverse", nil)

	h.Reverse(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, true, body["is_reversed"])

	feeRepo.AssertCalled(t, "UpdateStatus", mock.Anything, "fee-1", fee.StatusPending, (*time.Time)(nil))
}

func TestReversePayment_AlreadyReversed(t *testing.T) {
	paymentRepo := &testutil.MockPaymentRepo{}
	h := handler.NewPaymentHandler(paymentRepo, &testutil.MockFeeRepo{})

	p := &payment.Payment{ID: "pay-1", IsReversed: true}
	paymentRepo.On("FindByID", mock.Anything, "pay-1").Return(p, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "pay-1"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/payments/pay-1/reverse", nil)

	h.Reverse(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	paymentRepo.AssertNotCalled(t, "Reverse")
}

func TestReversePayment_NotFound(t *testing.T) {
	paymentRepo := &testutil.MockPaymentRepo{}
	h := handler.NewPaymentHandler(paymentRepo, &testutil.MockFeeRepo{})

	paymentRepo.On("FindByID", mock.Anything, "nonexistent").Return(nil, payment.ErrNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "nonexistent"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/payments/nonexistent/reverse", nil)

	h.Reverse(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
