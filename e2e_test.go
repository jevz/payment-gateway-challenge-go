//go:build e2e

// End-to-end tests against the real Mountebank bank simulator.
// Start it first with `docker-compose up`, then run:
//
//	go test -race -tags e2e ./...
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/api"
	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const simulatorURL = "http://localhost:8080"

func startGateway(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.New(simulatorURL).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, baseURL, cardNumber string) *http.Response {
	t.Helper()
	body, err := json.Marshal(models.PostPaymentRequest{
		CardNumber:  cardNumber,
		ExpiryMonth: 4,
		ExpiryYear:  2030,
		Currency:    "GBP",
		Amount:      100,
		Cvv:         "123",
	})
	require.NoError(t, err)
	resp, err := http.Post(baseURL+"/api/payments", "application/json", bytes.NewReader(body))
	require.NoError(t, err, "is the simulator running? start it with `docker-compose up`")
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestE2EAuthorizedCard(t *testing.T) {
	srv := startGateway(t)

	resp := post(t, srv.URL, "2222405343248877") // odd ending -> authorized

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	var p models.PostPaymentResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&p))
	assert.Equal(t, "Authorized", p.PaymentStatus)
	assert.Equal(t, "8877", p.CardNumberLastFour)
}

func TestE2EDeclinedCard(t *testing.T) {
	srv := startGateway(t)

	resp := post(t, srv.URL, "2222405343248872") // even ending -> declined

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	var p models.PostPaymentResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&p))
	assert.Equal(t, "Declined", p.PaymentStatus)
}

func TestE2EBankErrorCardIs502NotDeclined(t *testing.T) {
	srv := startGateway(t)

	resp := post(t, srv.URL, "2222405343248870") // ends in 0 -> simulator 503

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode, "a bank 503 should map to 502, not 500 or Declined")
	var body struct {
		Status    string `json:"status"`
		Retryable *bool  `json:"retryable"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "bank_unavailable", body.Status)
}

func TestE2EPostThenGetRoundTrip(t *testing.T) {
	srv := startGateway(t)

	resp := post(t, srv.URL, "2222405343248877")
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var posted models.PostPaymentResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&posted))

	getResp, err := http.Get(srv.URL + "/api/payments/" + posted.Id)
	require.NoError(t, err)
	defer getResp.Body.Close()

	assert.Equal(t, http.StatusOK, getResp.StatusCode)
	var got models.GetPaymentResponse
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&got))
	assert.Equal(t, models.GetPaymentResponse(posted), got)
}
