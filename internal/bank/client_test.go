package bank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRequest() Request {
	return Request{
		CardNumber: "2222405343248877",
		ExpiryDate: "04/2030",
		Currency:   "GBP",
		Amount:     100,
		Cvv:        "123",
		Reference:  "pay-1",
	}
}

func TestAuthorizeSuccess(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/payments", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		_ = json.NewEncoder(w).Encode(Authorization{Authorized: true, AuthorizationCode: "abc-123"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, time.Second)
	auth, err := c.Authorize(context.Background(), testRequest())

	require.NoError(t, err)
	assert.True(t, auth.Authorized)
	assert.Equal(t, "abc-123", auth.AuthorizationCode)

	// The request body must match what the simulator expects.
	assert.Equal(t, "2222405343248877", received["card_number"], "the bank must receive the raw PAN, not a masked one")
	assert.Equal(t, "04/2030", received["expiry_date"])
	assert.Equal(t, "GBP", received["currency"])
	assert.EqualValues(t, 100, received["amount"])
	assert.Equal(t, "123", received["cvv"])
	assert.Equal(t, "pay-1", received["reference"])
}

func TestAuthorizeDeclined(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Authorization{Authorized: false})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, time.Second)
	auth, err := c.Authorize(context.Background(), testRequest())

	require.NoError(t, err, "a decline is not an error")
	assert.False(t, auth.Authorized)
}

func TestAuthorize503IsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, time.Second)
	_, err := c.Authorize(context.Background(), testRequest())

	assert.ErrorIs(t, err, ErrUnavailable)
	assert.NotErrorIs(t, err, ErrTimeout)
}

func TestAuthorizeConnectionRefusedIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // nothing is listening any more

	c := NewClient(srv.URL, time.Second)
	_, err := c.Authorize(context.Background(), testRequest())

	assert.ErrorIs(t, err, ErrUnavailable)
	assert.NotErrorIs(t, err, ErrTimeout)
}

func TestAuthorizeDeadlineExceededIsTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // never answer within the client's deadline
	}))
	defer srv.Close()
	defer close(release) // LIFO: unblock the handler before Close waits on it

	c := NewClient(srv.URL, 50*time.Millisecond)
	_, err := c.Authorize(context.Background(), testRequest())

	assert.ErrorIs(t, err, ErrTimeout)
	assert.NotErrorIs(t, err, ErrUnavailable)
}
