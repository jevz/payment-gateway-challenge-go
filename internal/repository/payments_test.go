package repository

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/payment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func record(id, idemKey string) payment.Payment {
	return payment.Payment{
		ID:             id,
		IdempotencyKey: idemKey,
		Status:         payment.StatusProcessing,
		CardLast4:      "8877",
		ExpiryMonth:    4,
		ExpiryYear:     2030,
		Currency:       "GBP",
		Amount:         100,
		CreatedAt:      time.Now(),
	}
}

func TestCreateAndGet(t *testing.T) {
	repo := NewPaymentsRepository()

	p, created := repo.Create(record("pay-1", ""))
	assert.True(t, created)

	got, ok := repo.Get("pay-1")
	require.True(t, ok)
	assert.Equal(t, p, got)

	_, ok = repo.Get("missing")
	assert.False(t, ok)
}

func TestUpdateReplacesRecord(t *testing.T) {
	repo := NewPaymentsRepository()
	p, _ := repo.Create(record("pay-1", ""))

	p.Status = payment.StatusAuthorized
	p.BankAuthCode = "auth-1"
	repo.Update(p)

	got, ok := repo.Get("pay-1")
	require.True(t, ok)
	assert.Equal(t, payment.StatusAuthorized, got.Status)
	assert.Equal(t, "auth-1", got.BankAuthCode)
}

func TestCreateClaimsIdempotencyKeyAtomically(t *testing.T) {
	repo := NewPaymentsRepository()

	first, created := repo.Create(record("pay-1", "key-1"))
	assert.True(t, created)

	existing, created := repo.Create(record("pay-2", "key-1"))
	assert.False(t, created, "second create with the same key should not succeed")
	assert.Equal(t, first, existing, "second create should return the existing payment")

	_, ok := repo.Get("pay-2")
	assert.False(t, ok, "the second record should not be stored")
}

func TestEmptyIdempotencyKeyNeverDeduplicates(t *testing.T) {
	repo := NewPaymentsRepository()

	_, created := repo.Create(record("pay-1", ""))
	assert.True(t, created)
	_, created = repo.Create(record("pay-2", ""))
	assert.True(t, created)
}

func TestDeleteReleasesIdempotencyClaim(t *testing.T) {
	repo := NewPaymentsRepository()
	repo.Create(record("pay-1", "key-1"))

	repo.Delete("pay-1")

	_, ok := repo.Get("pay-1")
	assert.False(t, ok)

	_, created := repo.Create(record("pay-2", "key-1"))
	assert.True(t, created, "a deleted payment must free its idempotency key")
}

// Goroutines racing on one idempotency key must produce exactly one successful
// create, and concurrent inserts under different keys must all succeed.
func TestConcurrentCreatesExactlyOneWinnerPerKey(t *testing.T) {
	repo := NewPaymentsRepository()

	const n = 50
	var wg sync.WaitGroup
	var winners sync.Map
	wins := 0
	var winsMu sync.Mutex

	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Even goroutines share one key; odd goroutines use their own.
			if i%2 == 0 {
				p, created := repo.Create(record(fmt.Sprintf("shared-%d", i), "shared-key"))
				winners.Store(p.ID, true)
				if created {
					winsMu.Lock()
					wins++
					winsMu.Unlock()
				}
			} else {
				_, created := repo.Create(record(fmt.Sprintf("solo-%d", i), ""))
				assert.True(t, created)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, wins, "exactly one create should succeed for the shared key")

	count := 0
	winners.Range(func(_, _ any) bool { count++; return true })
	assert.Equal(t, 1, count, "every caller should get the same payment back")
}
