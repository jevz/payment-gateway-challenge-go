package payment

import (
	"strings"
	"testing"
	"time"

	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/models"
	"github.com/stretchr/testify/assert"
)

// fixedNow is the reference date for expiry checks in tests.
var fixedNow = time.Date(2026, time.June, 15, 12, 0, 0, 0, time.UTC)

func validRequest() models.PostPaymentRequest {
	return models.PostPaymentRequest{
		CardNumber:  "2222405343248877",
		ExpiryMonth: 4,
		ExpiryYear:  2030,
		Currency:    "GBP",
		Amount:      100,
		Cvv:         "123",
	}
}

type testStruct struct {
	name       string
	mutate     func(r *models.PostPaymentRequest)
	wantFields []string // fields expected to fail; empty means valid
}

func TestValidate(t *testing.T) {
	tests := []testStruct{
		{
			name:   "valid request",
			mutate: func(r *models.PostPaymentRequest) {},
		},
		{
			name:       "card number 13 digits too short",
			mutate:     func(r *models.PostPaymentRequest) { r.CardNumber = "1234567890123" },
			wantFields: []string{"card_number"},
		},
		{
			name:   "card number 14 digits is valid",
			mutate: func(r *models.PostPaymentRequest) { r.CardNumber = "12345678901237" },
		},
		{
			name:   "card number 19 digits is valid",
			mutate: func(r *models.PostPaymentRequest) { r.CardNumber = "1234567890123456787" },
		},
		{
			name:       "card number 20 digits too long",
			mutate:     func(r *models.PostPaymentRequest) { r.CardNumber = "12345678901234567891" },
			wantFields: []string{"card_number"},
		},
		{
			name:       "card number non-numeric",
			mutate:     func(r *models.PostPaymentRequest) { r.CardNumber = "22224053432488ab" },
			wantFields: []string{"card_number"},
		},
		{
			name:       "card number missing",
			mutate:     func(r *models.PostPaymentRequest) { r.CardNumber = "" },
			wantFields: []string{"card_number"},
		},
		{
			name: "expiry in the past",
			mutate: func(r *models.PostPaymentRequest) {
				r.ExpiryMonth = 5
				r.ExpiryYear = 2026
			},
			wantFields: []string{"expiry_year"},
		},
		{
			name: "expiry this month is still valid",
			mutate: func(r *models.PostPaymentRequest) {
				r.ExpiryMonth = 6
				r.ExpiryYear = 2026
			},
		},
		{
			name:       "expiry month zero (missing)",
			mutate:     func(r *models.PostPaymentRequest) { r.ExpiryMonth = 0 },
			wantFields: []string{"expiry_month"},
		},
		{
			name:       "expiry month 13 invalid",
			mutate:     func(r *models.PostPaymentRequest) { r.ExpiryMonth = 13 },
			wantFields: []string{"expiry_month"},
		},
		{
			name:       "expiry year missing",
			mutate:     func(r *models.PostPaymentRequest) { r.ExpiryYear = 0 },
			wantFields: []string{"expiry_year"},
		},
		{
			name:       "currency not on allowlist",
			mutate:     func(r *models.PostPaymentRequest) { r.Currency = "JPY" },
			wantFields: []string{"currency"},
		},
		{
			name:       "currency wrong length",
			mutate:     func(r *models.PostPaymentRequest) { r.Currency = "GBPX" },
			wantFields: []string{"currency"},
		},
		{
			name:       "currency missing",
			mutate:     func(r *models.PostPaymentRequest) { r.Currency = "" },
			wantFields: []string{"currency"},
		},
		{
			name:       "amount missing (zero)",
			mutate:     func(r *models.PostPaymentRequest) { r.Amount = 0 },
			wantFields: []string{"amount"},
		},
		{
			name:       "amount negative",
			mutate:     func(r *models.PostPaymentRequest) { r.Amount = -100 },
			wantFields: []string{"amount"},
		},
		{
			name:       "cvv 2 digits too short",
			mutate:     func(r *models.PostPaymentRequest) { r.Cvv = "12" },
			wantFields: []string{"cvv"},
		},
		{
			name:   "cvv 3 digits valid",
			mutate: func(r *models.PostPaymentRequest) { r.Cvv = "123" },
		},
		{
			name:   "cvv 4 digits valid",
			mutate: func(r *models.PostPaymentRequest) { r.Cvv = "1234" },
		},
		{
			name:       "cvv 5 digits too long",
			mutate:     func(r *models.PostPaymentRequest) { r.Cvv = "12345" },
			wantFields: []string{"cvv"},
		},
		{
			name:       "cvv non-numeric",
			mutate:     func(r *models.PostPaymentRequest) { r.Cvv = "12a" },
			wantFields: []string{"cvv"},
		},
		{
			name:       "cvv leading zeros preserved and valid",
			mutate:     func(r *models.PostPaymentRequest) { r.Cvv = "041" },
			wantFields: nil,
		},
		{
			name: "multiple failures reported together",
			mutate: func(r *models.PostPaymentRequest) {
				r.CardNumber = "abc"
				r.Currency = "POUNDS"
				r.Amount = 0
				r.Cvv = ""
			},
			wantFields: []string{"card_number", "currency", "amount", "cvv"},
		},
		{
			name:       "empty request reports every field",
			mutate:     func(r *models.PostPaymentRequest) { *r = models.PostPaymentRequest{} },
			wantFields: []string{"card_number", "expiry_month", "expiry_year", "currency", "amount", "cvv"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			tt.mutate(&req)

			err := validate(req, fixedNow)

			if len(tt.wantFields) == 0 {
				assert.NoError(t, err)
				return
			}

			var vErr *ValidationError
			if assert.ErrorAs(t, err, &vErr) {
				got := make([]string, len(vErr.Fields))
				for i, f := range vErr.Fields {
					got[i] = f.Field
				}
				assert.ElementsMatch(t, tt.wantFields, got)
			}
		})
	}
}

func TestValidationErrorMessageListsAllFields(t *testing.T) {
	err := validate(models.PostPaymentRequest{}, fixedNow)
	assert.Error(t, err)
	for _, field := range []string{"card_number", "expiry_month", "expiry_year", "currency", "amount", "cvv"} {
		assert.True(t, strings.Contains(err.Error(), field), "error message should mention %s", field)
	}
}
