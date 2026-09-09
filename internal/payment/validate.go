package payment

import (
	"fmt"
	"strings"
	"time"

	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/models"
)

// FieldError is one field-level validation failure.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError lists every field that failed validation.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	msgs := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		msgs[i] = f.Field + ": " + f.Message
	}
	return "invalid payment request: " + strings.Join(msgs, "; ")
}

// allowedCurrencies is the set of accepted ISO 4217 currency codes.
var allowedCurrencies = map[string]bool{
	"GBP": true,
	"USD": true,
	"EUR": true,
}

// validate checks the request against the gateway's rules. Missing fields
// arrive as zero values and fail the same checks.
func validate(req models.PostPaymentRequest, now time.Time) error {
	var errs []FieldError
	addError := func(field, message string) {
		errs = append(errs, FieldError{Field: field, Message: message})
	}

	switch {
	case req.CardNumber == "":
		addError("card_number", "is required")
	case !isDigits(req.CardNumber):
		addError("card_number", "must contain only numeric characters")
	case len(req.CardNumber) < 14 || len(req.CardNumber) > 19:
		addError("card_number", "must be between 14 and 19 digits long")
	}

	monthValid := req.ExpiryMonth >= 1 && req.ExpiryMonth <= 12
	if !monthValid {
		addError("expiry_month", "is required and must be between 1 and 12")
	}
	switch {
	case req.ExpiryYear == 0:
		addError("expiry_year", "is required")
	case monthValid && expired(req.ExpiryMonth, req.ExpiryYear, now):
		// A card is valid through the last day of its expiry month.
		addError("expiry_year", fmt.Sprintf("expiry %02d/%d must be in the future", req.ExpiryMonth, req.ExpiryYear))
	}

	switch {
	case req.Currency == "":
		addError("currency", "is required")
	case len(req.Currency) != 3:
		addError("currency", "must be exactly 3 characters")
	case !allowedCurrencies[req.Currency]:
		addError("currency", "must be one of GBP, USD, EUR")
	}

	if req.Amount <= 0 {
		addError("amount", "is required and must be a positive integer in the currency's minor unit")
	}

	switch {
	case req.Cvv == "":
		addError("cvv", "is required")
	case !isDigits(req.Cvv):
		addError("cvv", "must contain only numeric characters")
	case len(req.Cvv) < 3 || len(req.Cvv) > 4:
		addError("cvv", "must be 3 or 4 digits long")
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

func expired(month, year int, now time.Time) bool {
	return year < now.Year() || (year == now.Year() && month < int(now.Month()))
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
