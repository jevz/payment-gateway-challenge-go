package payment

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/bank"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBank lets each test script the bank's answer and count calls.
type fakeBank struct {
	calls     atomic.Int64
	lastReq   bank.Request
	mu        sync.Mutex
	authorize func(req bank.Request) (bank.Authorization, error)
}

func (f *fakeBank) Authorize(_ context.Context, req bank.Request) (bank.Authorization, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.lastReq = req
	f.mu.Unlock()
	return f.authorize(req)
}

// fakeRepo is an in-memory Repository. The real one lives in the repository
// package, which imports this package, so it cannot be used here.
type fakeRepo struct {
	mu        sync.Mutex
	payments  map[string]Payment
	byIdemKey map[string]string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{payments: map[string]Payment{}, byIdemKey: map[string]string{}}
}

func (r *fakeRepo) Create(p Payment) (Payment, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.IdempotencyKey != "" {
		if id, ok := r.byIdemKey[p.IdempotencyKey]; ok {
			return r.payments[id], false
		}
		r.byIdemKey[p.IdempotencyKey] = p.ID
	}
	r.payments[p.ID] = p
	return p, true
}

func (r *fakeRepo) Update(p Payment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.payments[p.ID] = p
}

func (r *fakeRepo) Delete(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.payments[id]; ok && p.IdempotencyKey != "" {
		delete(r.byIdemKey, p.IdempotencyKey)
	}
	delete(r.payments, id)
}

func (r *fakeRepo) Get(id string) (Payment, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.payments[id]
	return p, ok
}

func authorizedBank() *fakeBank {
	return &fakeBank{authorize: func(bank.Request) (bank.Authorization, error) {
		return bank.Authorization{Authorized: true, AuthorizationCode: "auth-123"}, nil
	}}
}

func newTestService(b BankClient, r Repository) *Service {
	s := NewService(b, r)
	s.now = func() time.Time { return fixedNow }
	return s
}

func TestProcessPaymentAuthorized(t *testing.T) {
	fb := authorizedBank()
	repo := newFakeRepo()
	svc := newTestService(fb, repo)

	p, err := svc.ProcessPayment(context.Background(), validRequest(), "")
	require.NoError(t, err)

	assert.Equal(t, StatusAuthorized, p.Status)
	assert.Equal(t, "8877", p.CardLast4)
	assert.Equal(t, "auth-123", p.BankAuthCode)
	assert.NotEmpty(t, p.ID)

	stored, ok := repo.Get(p.ID)
	require.True(t, ok)
	assert.Equal(t, StatusAuthorized, stored.Status)

	// Check what was sent to the bank.
	assert.Equal(t, "2222405343248877", fb.lastReq.CardNumber)
	assert.Equal(t, "04/2030", fb.lastReq.ExpiryDate)
	assert.Equal(t, p.ID, fb.lastReq.Reference)
}

func TestProcessPaymentDeclined(t *testing.T) {
	fb := &fakeBank{authorize: func(bank.Request) (bank.Authorization, error) {
		return bank.Authorization{Authorized: false}, nil
	}}
	repo := newFakeRepo()
	svc := newTestService(fb, repo)

	p, err := svc.ProcessPayment(context.Background(), validRequest(), "")
	require.NoError(t, err) // a decline is not an error
	assert.Equal(t, StatusDeclined, p.Status)
	assert.Empty(t, p.BankAuthCode)

	stored, ok := repo.Get(p.ID)
	require.True(t, ok)
	assert.Equal(t, StatusDeclined, stored.Status)
}

func TestProcessPaymentRejectedNeverCallsBank(t *testing.T) {
	fb := authorizedBank()
	svc := newTestService(fb, newFakeRepo())

	req := validRequest()
	req.CardNumber = "not-a-card"

	_, err := svc.ProcessPayment(context.Background(), req, "")

	var vErr *ValidationError
	assert.ErrorAs(t, err, &vErr)
	assert.EqualValues(t, 0, fb.calls.Load(), "a rejected payment should not call the bank")
}

func TestIdempotentReplayReturnsStoredResultWithoutSecondBankCall(t *testing.T) {
	fb := authorizedBank()
	svc := newTestService(fb, newFakeRepo())

	first, err := svc.ProcessPayment(context.Background(), validRequest(), "key-1")
	require.NoError(t, err)

	second, err := svc.ProcessPayment(context.Background(), validRequest(), "key-1")
	require.NoError(t, err)

	assert.Equal(t, first, second, "replay should return the stored result")
	assert.EqualValues(t, 1, fb.calls.Load(), "replay must not call the bank again")
}

func TestIdempotencyKeyReuseWithDifferentBodyIsRefused(t *testing.T) {
	fb := authorizedBank()
	svc := newTestService(fb, newFakeRepo())

	_, err := svc.ProcessPayment(context.Background(), validRequest(), "key-1")
	require.NoError(t, err)

	changed := validRequest()
	changed.Amount = 999

	_, err = svc.ProcessPayment(context.Background(), changed, "key-1")
	assert.ErrorIs(t, err, ErrIdempotencyKeyReuse)
	assert.EqualValues(t, 1, fb.calls.Load(), "a refused reuse should not call the bank")
}

func TestDuplicateWhileInFlightGetsProcessingNotSecondBankCall(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	fb := &fakeBank{authorize: func(bank.Request) (bank.Authorization, error) {
		close(entered)
		<-release
		return bank.Authorization{Authorized: true, AuthorizationCode: "auth-123"}, nil
	}}
	svc := newTestService(fb, newFakeRepo())

	firstDone := make(chan Payment)
	go func() {
		p, err := svc.ProcessPayment(context.Background(), validRequest(), "key-1")
		assert.NoError(t, err)
		firstDone <- p
	}()

	<-entered // the first request is now inside the bank call

	p, err := svc.ProcessPayment(context.Background(), validRequest(), "key-1")
	assert.ErrorIs(t, err, ErrDuplicateInFlight)
	assert.Equal(t, StatusProcessing, p.Status)
	assert.NotEmpty(t, p.ID, "the duplicate should return the in-flight payment ID")

	close(release)
	first := <-firstDone
	assert.Equal(t, first.ID, p.ID)
	assert.EqualValues(t, 1, fb.calls.Load(), "the duplicate should not call the bank")
}

// Concurrent requests sharing one idempotency key must produce one bank call.
func TestConcurrentDuplicatesCauseExactlyOneBankCall(t *testing.T) {
	fb := authorizedBank()
	svc := newTestService(fb, newFakeRepo())

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.ProcessPayment(context.Background(), validRequest(), "storm-key")
			// The other requests get ErrDuplicateInFlight or the replayed result.
			if err != nil {
				assert.ErrorIs(t, err, ErrDuplicateInFlight)
			}
		}()
	}
	wg.Wait()

	assert.EqualValues(t, 1, fb.calls.Load(), "exactly one bank call for %d concurrent duplicates", n)
}

func TestBankTimeoutLeavesPaymentProcessing(t *testing.T) {
	fb := &fakeBank{authorize: func(bank.Request) (bank.Authorization, error) {
		return bank.Authorization{}, fmt.Errorf("bank: %w", bank.ErrTimeout)
	}}
	repo := newFakeRepo()
	svc := newTestService(fb, repo)

	p, err := svc.ProcessPayment(context.Background(), validRequest(), "key-1")
	assert.ErrorIs(t, err, bank.ErrTimeout)
	require.NotEmpty(t, p.ID)

	// The record is kept so GET can report the payment as Processing.
	stored, ok := svc.GetPayment(p.ID)
	require.True(t, ok)
	assert.Equal(t, StatusProcessing, stored.Status)
}

func TestBankUnavailableStoresNothingAndFreesTheKey(t *testing.T) {
	fb := &fakeBank{authorize: func(bank.Request) (bank.Authorization, error) {
		return bank.Authorization{}, fmt.Errorf("bank: status 503: %w", bank.ErrUnavailable)
	}}
	repo := newFakeRepo()
	svc := newTestService(fb, repo)

	p, err := svc.ProcessPayment(context.Background(), validRequest(), "key-1")
	assert.ErrorIs(t, err, bank.ErrUnavailable)
	assert.Empty(t, p.ID)

	// The bank did not process the payment, so a retry with the same key
	// should reach the bank again.
	fb.authorize = func(bank.Request) (bank.Authorization, error) {
		return bank.Authorization{Authorized: true, AuthorizationCode: "auth-retry"}, nil
	}
	retried, err := svc.ProcessPayment(context.Background(), validRequest(), "key-1")
	require.NoError(t, err)
	assert.Equal(t, StatusAuthorized, retried.Status)
	assert.EqualValues(t, 2, fb.calls.Load())
}
