package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/bank"
	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/models"
	"github.com/google/uuid"
)

var (
	// ErrIdempotencyKeyReuse is returned when an Idempotency-Key is reused
	// with a different request body.
	ErrIdempotencyKeyReuse = errors.New("idempotency key reused with a different request payload")

	// ErrDuplicateInFlight is returned when a request with the same
	// Idempotency-Key is still being processed.
	ErrDuplicateInFlight = errors.New("a payment with this idempotency key is still processing")
)

// BankClient is the part of the bank client the service uses. It is defined
// here so tests can substitute a fake.
type BankClient interface {
	Authorize(ctx context.Context, req bank.Request) (bank.Authorization, error)
}

// Repository stores payments. Create must check and claim the idempotency
// key atomically; see repository.PaymentsRepository.
type Repository interface {
	Create(p Payment) (Payment, bool)
	Update(p Payment)
	Delete(id string)
	Get(id string) (Payment, bool)
}

// Service processes payments: validate, store, call the bank, record the
// result.
type Service struct {
	bank BankClient
	repo Repository
	now  func() time.Time
}

func NewService(bankClient BankClient, repo Repository) *Service {
	return &Service{
		bank: bankClient,
		repo: repo,
		now:  time.Now,
	}
}

// ProcessPayment handles a POST /api/payments request.
//
// The payment is stored as Processing before the bank is called, so that if
// the bank times out the payment can still be looked up rather than being
// lost.
//
// On ErrDuplicateInFlight and bank.ErrTimeout the returned Payment is the
// stored Processing record. On any other error it is the zero value.
func (s *Service) ProcessPayment(ctx context.Context, req models.PostPaymentRequest, idempotencyKey string) (Payment, error) {
	if err := validate(req, s.now()); err != nil {
		return Payment{}, err
	}

	pan := PAN(req.CardNumber)
	p := Payment{
		ID:             uuid.NewString(),
		IdempotencyKey: idempotencyKey,
		Status:         StatusProcessing,
		CardLast4:      pan.LastFour(),
		ExpiryMonth:    req.ExpiryMonth,
		ExpiryYear:     req.ExpiryYear,
		Currency:       req.Currency,
		Amount:         req.Amount,
		CreatedAt:      s.now(),
	}
	if idempotencyKey != "" {
		p.RequestHash = hashRequest(req)
	}

	stored, created := s.repo.Create(p)
	if !created {
		// Another request with the same key got there first.
		if stored.RequestHash != p.RequestHash {
			return Payment{}, ErrIdempotencyKeyReuse
		}
		if stored.Status == StatusProcessing {
			return stored, ErrDuplicateInFlight
		}
		return stored, nil // replay of a completed payment
	}

	auth, err := s.bank.Authorize(ctx, bank.Request{
		CardNumber: string(pan), // the only place the raw card number is used
		ExpiryDate: fmt.Sprintf("%02d/%04d", req.ExpiryMonth, req.ExpiryYear),
		Currency:   req.Currency,
		Amount:     req.Amount,
		Cvv:        req.Cvv,
		Reference:  p.ID,
	})
	switch {
	case errors.Is(err, bank.ErrTimeout):
		// Outcome unknown. Keep the Processing record so it can be polled.
		return p, fmt.Errorf("authorizing payment %s: %w", p.ID, err)
	case err != nil:
		// The bank did not process the payment. Remove the record so a retry
		// with the same key starts clean.
		s.repo.Delete(p.ID)
		return Payment{}, fmt.Errorf("authorizing payment: %w", err)
	}

	if auth.Authorized {
		p.Status = StatusAuthorized
		p.BankAuthCode = auth.AuthorizationCode
	} else {
		p.Status = StatusDeclined
	}
	s.repo.Update(p)
	return p, nil
}

// GetPayment returns the payment with the given ID, if it exists.
func (s *Service) GetPayment(id string) (Payment, bool) {
	return s.repo.Get(id)
}

// hashRequest returns a SHA-256 hash of the request body, used to detect an
// idempotency key being reused with a different body.
func hashRequest(req models.PostPaymentRequest) string {
	b, _ := json.Marshal(req) // marshalling a plain struct cannot fail
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
