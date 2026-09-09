package payment

import "time"

// Status is the state of a payment.
type Status string

const (
	// StatusProcessing means the bank's answer is not known yet: the call is
	// in flight, or it timed out.
	StatusProcessing Status = "Processing"
	StatusAuthorized Status = "Authorized"
	StatusDeclined   Status = "Declined"
)

// Payment is the stored payment record. IdempotencyKey, RequestHash and
// BankAuthCode are internal and not returned to the merchant. There is no
// CVV or full card number field; only the last four digits are kept.
type Payment struct {
	ID             string
	IdempotencyKey string
	RequestHash    string
	Status         Status
	CardLast4      string
	ExpiryMonth    int
	ExpiryYear     int
	Currency       string
	Amount         int
	BankAuthCode   string
	CreatedAt      time.Time
}
