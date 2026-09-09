package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/bank"
	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/models"
	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/payment"
	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/repository"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBankServer behaves like the simulator: an odd last digit is authorized,
// even is declined, and 0 returns a 503.
func fakeBankServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req bank.Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.NotContains(t, req.CardNumber, "*", "the gateway must send the raw PAN, not a masked one")

		last := req.CardNumber[len(req.CardNumber)-1]
		switch {
		case last == '0':
			w.WriteHeader(http.StatusServiceUnavailable)
		case (last-'0')%2 == 1:
			_ = json.NewEncoder(w).Encode(bank.Authorization{Authorized: true, AuthorizationCode: "11111111-2222-4333-8444-555555555555"})
		default:
			_ = json.NewEncoder(w).Encode(bank.Authorization{Authorized: false})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTestRouter wires the real service, repository and bank client against a
// fake bank server.
func newTestRouter(t *testing.T, bankURL string) chi.Router {
	t.Helper()
	svc := payment.NewService(bank.NewClient(bankURL, time.Second), repository.NewPaymentsRepository())
	h := NewPaymentsHandler(svc)

	r := chi.NewRouter()
	r.Post("/api/payments", h.PostHandler())
	r.Get("/api/payments/{id}", h.GetHandler())
	return r
}

func postPayment(t *testing.T, r chi.Router, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(body))
	req := httptest.NewRequest(http.MethodPost, "/api/payments", &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func paymentRequest(cardNumber string) models.PostPaymentRequest {
	return models.PostPaymentRequest{
		CardNumber:  cardNumber,
		ExpiryMonth: 4,
		ExpiryYear:  2030,
		Currency:    "GBP",
		Amount:      100,
		Cvv:         "123",
	}
}

func TestPostPaymentAuthorized(t *testing.T) {
	r := newTestRouter(t, fakeBankServer(t).URL)

	w := postPayment(t, r, paymentRequest("2222405343248877"), nil)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp models.PostPaymentResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp.Id)
	assert.Equal(t, "Authorized", resp.PaymentStatus)
	assert.Equal(t, "8877", resp.CardNumberLastFour)
	assert.Equal(t, 4, resp.ExpiryMonth)
	assert.Equal(t, 2030, resp.ExpiryYear)
	assert.Equal(t, "GBP", resp.Currency)
	assert.Equal(t, 100, resp.Amount)
}

func TestPostPaymentResponseJSONShape(t *testing.T) {
	r := newTestRouter(t, fakeBankServer(t).URL)

	w := postPayment(t, r, paymentRequest("2222405343248877"), nil)
	require.Equal(t, http.StatusCreated, w.Code)

	body := w.Body.String()
	for _, key := range []string{`"id"`, `"payment_status"`, `"card_number_last_four"`, `"expiry_month"`, `"expiry_year"`, `"currency"`, `"amount"`} {
		assert.Contains(t, body, key)
	}
	// last four is a string and the full card number is not in the response
	assert.Contains(t, body, `"card_number_last_four":"8877"`)
	assert.NotContains(t, body, "2222405343248877")
	assert.NotContains(t, strings.ToLower(body), "cvv")
}

func TestPostPaymentDeclined(t *testing.T) {
	r := newTestRouter(t, fakeBankServer(t).URL)

	w := postPayment(t, r, paymentRequest("2222405343248872"), nil)

	assert.Equal(t, http.StatusCreated, w.Code, "a decline is not an error")

	var resp models.PostPaymentResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Declined", resp.PaymentStatus)
}

func TestPostPaymentRejectedWithAllFieldErrors(t *testing.T) {
	r := newTestRouter(t, fakeBankServer(t).URL)

	req := paymentRequest("bad")
	req.Currency = "XYZ"
	req.Cvv = "12345"

	w := postPayment(t, r, req, nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp struct {
		Status string `json:"status"`
		Errors []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "rejected", resp.Status)

	fields := make([]string, len(resp.Errors))
	for i, e := range resp.Errors {
		fields[i] = e.Field
	}
	assert.ElementsMatch(t, []string{"card_number", "currency", "cvv"}, fields,
		"all validation failures must be reported in one response")
}

func TestPostPaymentMalformedJSONRejected(t *testing.T) {
	r := newTestRouter(t, fakeBankServer(t).URL)

	req := httptest.NewRequest(http.MethodPost, "/api/payments", strings.NewReader("{not json"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostPaymentBankUnavailableIs502NotDeclined(t *testing.T) {
	r := newTestRouter(t, fakeBankServer(t).URL)

	w := postPayment(t, r, paymentRequest("2222405343248870"), nil) // ends in 0 -> bank 503

	assert.Equal(t, http.StatusBadGateway, w.Code, "a bank 503 should be a 502, not a 500 or a decline")

	var resp struct {
		Status    string `json:"status"`
		Retryable *bool  `json:"retryable"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "bank_unavailable", resp.Status)
	require.NotNil(t, resp.Retryable)
	assert.True(t, *resp.Retryable)
}

func TestPostPaymentBankTimeoutLeavesPaymentRetrievable(t *testing.T) {
	release := make(chan struct{})
	slowBank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // never answer within the client's deadline
	}))
	t.Cleanup(slowBank.Close)
	t.Cleanup(func() { close(release) }) // LIFO: unblock the handler before Close waits on it

	svc := payment.NewService(bank.NewClient(slowBank.URL, 50*time.Millisecond), repository.NewPaymentsRepository())
	h := NewPaymentsHandler(svc)
	r := chi.NewRouter()
	r.Post("/api/payments", h.PostHandler())
	r.Get("/api/payments/{id}", h.GetHandler())

	w := postPayment(t, r, paymentRequest("2222405343248877"), nil)

	assert.Equal(t, http.StatusBadGateway, w.Code)

	var resp struct {
		Status    string `json:"status"`
		PaymentID string `json:"payment_id"`
		Retryable *bool  `json:"retryable"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "processing", resp.Status, "a timeout is not a decline")
	require.NotNil(t, resp.Retryable)
	assert.False(t, *resp.Retryable, "an unknown outcome must not be marked retryable")
	require.NotEmpty(t, resp.PaymentID)

	// The payment should be retrievable as Processing.
	getReq := httptest.NewRequest(http.MethodGet, "/api/payments/"+resp.PaymentID, nil)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)

	assert.Equal(t, http.StatusOK, getW.Code)
	var got models.GetPaymentResponse
	require.NoError(t, json.NewDecoder(getW.Body).Decode(&got))
	assert.Equal(t, "Processing", got.PaymentStatus)
}

func TestIdempotencyReplayAndConflictOverHTTP(t *testing.T) {
	r := newTestRouter(t, fakeBankServer(t).URL)
	headers := map[string]string{"Idempotency-Key": "key-1"}

	first := postPayment(t, r, paymentRequest("2222405343248877"), headers)
	require.Equal(t, http.StatusCreated, first.Code)

	replay := postPayment(t, r, paymentRequest("2222405343248877"), headers)
	assert.Equal(t, http.StatusCreated, replay.Code)
	assert.JSONEq(t, first.Body.String(), replay.Body.String(), "a replay should return the stored result")

	changed := paymentRequest("2222405343248877")
	changed.Amount = 999
	conflict := postPayment(t, r, changed, headers)
	assert.Equal(t, http.StatusUnprocessableEntity, conflict.Code,
		"reusing an idempotency key with a different payload must be refused")
}

func TestPostThenGetRoundTrip(t *testing.T) {
	r := newTestRouter(t, fakeBankServer(t).URL)

	w := postPayment(t, r, paymentRequest("2222405343248877"), nil)
	require.Equal(t, http.StatusCreated, w.Code)
	var posted models.PostPaymentResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&posted))

	req := httptest.NewRequest(http.MethodGet, "/api/payments/"+posted.Id, nil)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, req)

	assert.Equal(t, http.StatusOK, getW.Code)
	var got models.GetPaymentResponse
	require.NoError(t, json.NewDecoder(getW.Body).Decode(&got))
	assert.Equal(t, models.GetPaymentResponse(posted), got)
}

func TestGetPaymentNotFoundIs404(t *testing.T) {
	r := newTestRouter(t, fakeBankServer(t).URL)

	req := httptest.NewRequest(http.MethodGet, "/api/payments/does-not-exist", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestConcurrentPostsAreRaceFree(t *testing.T) {
	r := newTestRouter(t, fakeBankServer(t).URL)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		i := i
		go func() {
			defer func() { done <- struct{}{} }()
			w := postPayment(t, r, paymentRequest(fmt.Sprintf("22224053432488%02d", i*2+1)), nil)
			assert.Equal(t, http.StatusCreated, w.Code)
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
