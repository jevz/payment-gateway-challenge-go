// Package handlers contains the HTTP handlers for the payments API.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/bank"
	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/models"
	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/payment"
	"github.com/go-chi/chi/v5"
)

type PaymentsHandler struct {
	service *payment.Service
}

func NewPaymentsHandler(service *payment.Service) *PaymentsHandler {
	return &PaymentsHandler{service: service}
}

// errorResponse is the body of every non-2xx response.
type errorResponse struct {
	Status       string               `json:"status"`
	ErrorMessage string               `json:"error_message,omitempty"`
	Errors       []payment.FieldError `json:"errors,omitempty"`
	PaymentID    string               `json:"payment_id,omitempty"`
	Retryable    *bool                `json:"retryable,omitempty"`
}

func boolPtr(b bool) *bool { return &b }

// PostHandler handles POST /api/payments.
//
//	@Summary		Process a card payment
//	@Description	Validates the request, sends it to the acquiring bank and stores the result.
//	@Tags			payments
//	@Accept			json
//	@Produce		json
//	@Param			Idempotency-Key	header		string						false	"Optional key that makes retries safe"
//	@Param			payment			body		models.PostPaymentRequest	true	"Payment request"
//	@Success		201				{object}	models.PostPaymentResponse	"Authorized or Declined"
//	@Success		202				{object}	errorResponse				"Duplicate Idempotency-Key still in flight; poll GET /api/payments/{id}"
//	@Failure		400				{object}	errorResponse				"Failed validation; the bank was not called"
//	@Failure		422				{object}	errorResponse				"Idempotency-Key reused with a different body"
//	@Failure		502				{object}	errorResponse				"Bank unavailable or timed out"
//	@Router			/api/payments [post]
func (h *PaymentsHandler) PostHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req models.PostPaymentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Status:       "rejected",
				ErrorMessage: "request body must be valid JSON matching the payment schema",
			})
			return
		}

		p, err := h.service.ProcessPayment(r.Context(), req, r.Header.Get("Idempotency-Key"))

		var validationErr *payment.ValidationError
		switch {
		case err == nil:
			writeJSON(w, http.StatusCreated, toResponse(p))
		case errors.As(err, &validationErr):
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Status:       "rejected",
				ErrorMessage: "the payment request failed validation and was not sent to the bank",
				Errors:       validationErr.Fields,
			})
		case errors.Is(err, payment.ErrIdempotencyKeyReuse):
			writeJSON(w, http.StatusUnprocessableEntity, errorResponse{
				Status:       "idempotency_key_reuse",
				ErrorMessage: "this Idempotency-Key was already used with a different request payload",
			})
		case errors.Is(err, payment.ErrDuplicateInFlight):
			writeJSON(w, http.StatusAccepted, errorResponse{
				Status:       "processing",
				ErrorMessage: "a payment with this Idempotency-Key is already being processed; poll GET /api/payments/{id}",
				PaymentID:    p.ID,
			})
		case errors.Is(err, bank.ErrTimeout):
			writeJSON(w, http.StatusBadGateway, errorResponse{
				Status:       "processing",
				ErrorMessage: "the bank did not respond in time; the payment outcome is unknown, poll GET /api/payments/{id}",
				PaymentID:    p.ID,
				Retryable:    boolPtr(false),
			})
		case errors.Is(err, bank.ErrUnavailable):
			writeJSON(w, http.StatusBadGateway, errorResponse{
				Status:       "bank_unavailable",
				ErrorMessage: "the acquiring bank is unavailable; the payment was not processed and can be retried",
				Retryable:    boolPtr(true),
			})
		default:
			writeJSON(w, http.StatusInternalServerError, errorResponse{
				Status:       "internal_error",
				ErrorMessage: "an unexpected error occurred",
			})
		}
	}
}

// GetHandler handles GET /api/payments/{id}.
//
//	@Summary	Retrieve a payment
//	@Tags		payments
//	@Produce	json
//	@Param		id	path		string	true	"Payment ID"
//	@Success	200	{object}	models.GetPaymentResponse
//	@Failure	404	{object}	errorResponse	"No payment with this ID"
//	@Router		/api/payments/{id} [get]
func (h *PaymentsHandler) GetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		p, ok := h.service.GetPayment(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, errorResponse{
				Status:       "not_found",
				ErrorMessage: "no payment exists with this id",
			})
			return
		}
		writeJSON(w, http.StatusOK, toResponse(p))
	}
}

func toResponse(p payment.Payment) models.PostPaymentResponse {
	return models.PostPaymentResponse{
		Id:                 p.ID,
		PaymentStatus:      string(p.Status),
		CardNumberLastFour: p.CardLast4,
		ExpiryMonth:        p.ExpiryMonth,
		ExpiryYear:         p.ExpiryYear,
		Currency:           p.Currency,
		Amount:             p.Amount,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
