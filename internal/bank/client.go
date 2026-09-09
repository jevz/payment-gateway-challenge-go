// Package bank is the HTTP client for the acquiring bank.
package bank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	// ErrUnavailable means the bank could not be reached or returned a non-200
	// status. The payment was not processed and can be retried.
	ErrUnavailable = errors.New("bank unavailable")

	// ErrTimeout means the bank did not answer within the deadline. The request
	// may have been processed, so the outcome is unknown.
	ErrTimeout = errors.New("bank timeout")
)

// Request is the body sent to the bank's /payments endpoint.
type Request struct {
	CardNumber string `json:"card_number"`
	ExpiryDate string `json:"expiry_date"` // "MM/YYYY"
	Currency   string `json:"currency"`
	Amount     int    `json:"amount"`
	Cvv        string `json:"cvv"`
	// Reference is the gateway payment ID, so bank records can be matched
	// back to ours.
	Reference string `json:"reference"`
}

// Authorization is the bank's response. A decline is Authorized=false, not an
// error.
type Authorization struct {
	Authorized        bool   `json:"authorized"`
	AuthorizationCode string `json:"authorization_code"`
}

type Client struct {
	baseURL string
	timeout time.Duration
	http    *http.Client
}

// NewClient returns a client whose calls are bounded by timeout.
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		timeout: timeout,
		http:    &http.Client{},
	}
}

// Authorize posts the payment to the bank. Failures are wrapped as ErrTimeout
// or ErrUnavailable so callers can match them with errors.Is.
func (c *Client) Authorize(ctx context.Context, req Request) (Authorization, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	body, err := json.Marshal(req)
	if err != nil {
		return Authorization{}, fmt.Errorf("bank: encoding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/payments", bytes.NewReader(body))
	if err != nil {
		return Authorization{}, fmt.Errorf("bank: building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return Authorization{}, fmt.Errorf("bank: %v: %w", err, ErrTimeout)
		}
		return Authorization{}, fmt.Errorf("bank: %v: %w", err, ErrUnavailable)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var auth Authorization
		if err := json.NewDecoder(resp.Body).Decode(&auth); err != nil {
			return Authorization{}, fmt.Errorf("bank: decoding response: %w", err)
		}
		return auth, nil
	default:
		// Any non-200 status means the bank did not process the payment.
		return Authorization{}, fmt.Errorf("bank: status %d: %w", resp.StatusCode, ErrUnavailable)
	}
}
