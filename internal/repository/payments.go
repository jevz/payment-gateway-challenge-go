// Package repository is the in-memory payment store.
package repository

import (
	"sync"

	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/payment"
)

// PaymentsRepository is an in-memory implementation of payment.Repository.
type PaymentsRepository struct {
	mu        sync.RWMutex
	payments  map[string]payment.Payment
	byIdemKey map[string]string // idempotency key -> payment ID
}

func NewPaymentsRepository() *PaymentsRepository {
	return &PaymentsRepository{
		payments:  map[string]payment.Payment{},
		byIdemKey: map[string]string{},
	}
}

// Create stores p and returns (p, true). If p.IdempotencyKey is already in
// use it stores nothing and returns the existing payment and false. The
// check and the insert happen under one lock, so concurrent requests with
// the same key get exactly one winner. An empty key is not deduplicated.
func (r *PaymentsRepository) Create(p payment.Payment) (payment.Payment, bool) {
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

// Update replaces the stored record for p.ID.
func (r *PaymentsRepository) Update(p payment.Payment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.payments[p.ID] = p
}

// Delete removes a payment and frees its idempotency key.
func (r *PaymentsRepository) Delete(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.payments[id]; ok && p.IdempotencyKey != "" {
		delete(r.byIdemKey, p.IdempotencyKey)
	}
	delete(r.payments, id)
}

// Get returns the payment with the given ID, if it exists.
func (r *PaymentsRepository) Get(id string) (payment.Payment, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.payments[id]
	return p, ok
}
