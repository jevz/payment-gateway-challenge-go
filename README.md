# Payment Gateway (Go)

A payment gateway that validates card payment requests, forwards valid ones
to the acquiring bank simulator, stores the result, and lets merchants
retrieve payments by ID.

## Running it

```bash
# start the bank simulator (Mountebank, port 8080)
docker-compose up

# start the gateway (port 8090)
go run .

# unit and handler tests
go test -race ./...

# end-to-end tests against the simulator (needs docker-compose up)
go test -race -tags e2e ./...
```

`BANK_URL` overrides the simulator address (default `http://localhost:8080`).
Swagger UI is at `http://localhost:8090/swagger/index.html`. The spec is
generated from annotations on the handlers; after changing them, run
`swag init` (`go install github.com/swaggo/swag/cmd/swag@v1.16.2`) to
regenerate the `docs` package.

## API

```
POST /api/payments        process a card payment
GET  /api/payments/{id}   retrieve a payment
```

Example:

```bash
curl -s -X POST localhost:8090/api/payments \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: order-1001' \
  -d '{"card_number":"2222405343248877","expiry_month":4,"expiry_year":2030,"currency":"GBP","amount":100,"cvv":"123"}'
```

```json
{
  "id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  "payment_status": "Authorized",
  "card_number_last_four": "8877",
  "expiry_month": 4,
  "expiry_year": 2030,
  "currency": "GBP",
  "amount": 100
}
```

Response codes for POST:

| Code | Meaning |
|------|---------|
| 201  | Payment processed. `payment_status` is `Authorized` or `Declined`. |
| 400  | Request failed validation. The bank was not called. The body lists every failing field. |
| 202  | A request with the same `Idempotency-Key` is still in flight. The body carries the payment ID to poll. |
| 422  | The `Idempotency-Key` was already used with a different request body. |
| 502  | The bank was unavailable or timed out. See below. |

GET returns 200 with the payment, or 404 if the ID is unknown.

Every non-2xx body has the same shape: a `status` string, an
`error_message`, and where relevant `errors` (per-field detail),
`payment_id` and `retryable`.

## Structure

```
main.go                  entry point, reads BANK_URL, starts the server
internal/api             router and dependency wiring
internal/handlers        HTTP handlers: decode requests, map errors to status codes
internal/payment         validation, the payment service, the PAN type
internal/bank            HTTP client for the bank, with timeout and error classification
internal/repository      in-memory payment store
internal/models          request and response types
docs                     generated Swagger spec (from the template)
```

Handlers call the service. The service calls the bank and the repository
through two interfaces, `payment.BankClient` and `payment.Repository`, which
are defined in the payment package next to the code that uses them. That is
what lets the service tests run against a fake bank, and the handler tests
run the real service against a fake bank HTTP server.

Storage is in memory, as the brief allows. Replacing it with a database means
implementing the four methods of `payment.Repository`. The idempotency check
described below would become a unique index on the key.

## Rejected, Declined and Authorized

| Outcome    | Meaning                        | Bank called | Stored | HTTP |
|------------|--------------------------------|-------------|--------|------|
| Authorized | The bank approved the payment  | yes         | yes    | 201  |
| Declined   | The bank refused the payment   | yes         | yes    | 201  |
| Rejected   | The request failed validation  | no          | no     | 400  |

A decline is a normal outcome, so it returns 201 with
`"payment_status": "Declined"`. A rejection never reaches the bank, and the
400 body reports every failing field at once rather than one per attempt.

## Bank failures

The bank client turns failures into two errors so the service can treat them
differently:

- `bank.ErrUnavailable`: connection refused, or any non-200 status such as
  503. The bank did not process the payment. The gateway returns 502 with
  `"status": "bank_unavailable"` and `"retryable": true`. No payment record
  is kept and the idempotency key is released, so a retry starts clean.
- `bank.ErrTimeout`: no answer within the deadline. The request may have
  reached the bank, so the outcome is unknown. The gateway returns 502 with
  `"status": "processing"`, `"retryable": false` and the payment ID. The
  payment stays stored as `Processing`.

A timeout is not reported as a decline, because the bank did not say no. It
is also not retried automatically, because a retry after an unknown outcome
can charge the card twice. Retrying is left to the merchant, and is safe when
they send the same `Idempotency-Key`.

Each bank call has a 3 second deadline via `context.WithTimeout`. A bank
failure is a 502 rather than a 500 because nothing went wrong inside the
gateway.

### The Processing state

The payment record is written as `Processing` before the bank is called and
updated to `Authorized` or `Declined` afterwards. If the bank times out, the
record stays as `Processing`, so `GET /api/payments/{id}` can return it rather
than reporting that the payment does not exist. Without this, a customer
could be charged for a payment the gateway has no record of.

Timed-out payments stay in `Processing` indefinitely in this implementation.
A production system would reconcile them against the bank using the gateway
payment ID, which is already sent to the bank as `reference` on every
request.

## Idempotency

The brief does not ask for this. It is included because a payments API
without it makes double charges easy to cause with a retry.

- Merchants can send an `Idempotency-Key` header. It is optional so that
  clients that do not send one still work.
- The repository's `Create` checks for an existing key and inserts under one
  lock. Under concurrent requests exactly one succeeds and the others are
  given the first request's record. A separate check followed by an insert
  could not guarantee this.
- A duplicate never fails and never causes a second bank call. If the first
  payment has finished, the stored result is returned with 201. If it is
  still `Processing`, the response is 202 with the payment ID.
- A SHA-256 hash of the request body is stored with the key. If the same key arrives with a different body the gateway returns 422 and processes nothing.

## Card data

- The card number is held in a `PAN` type whose `String` and `MarshalJSON`
  methods return the masked form `**** **** **** 8877`, so printing or
  JSON-encoding it can only produce that. The raw number is only reachable
  with an explicit `string(pan)` conversion, which appears once, when
  building the bank request. The methods have value receivers so the masking also applies when a PAN is held by value in another struct.
- The stored record has no CVV field and no full card number field. Only the last four  digits and the expiry are kept and returned.
- The request logging middleware logs method, path, status and latency.
  Request bodies are not logged.

## Changes from the template

- `cvv` and `card_number_last_four` are strings rather than ints so leading
  zeros survive.
- The request model takes a full `card_number`. The template's request type
  only had the last four digits, which is not enough to process a payment.
- The repository/store is a map guarded by a `sync.RWMutex` instead of an
  unsynchronised slice. The slice fails under `go test -race` with concurrent
  POSTs.
- GET on an unknown ID returns 404 rather than 204. The template's own test
  already expected 404.

## Not included

- Luhn validation. The brief lists length and digit checks only, and the
  simulator's test cards do not pass Luhn, so a Luhn check would reject the
  examples in the brief.
- Automatic retries of bank calls.
- A real database, reconciliation of stuck payments, authentication, rate
  limiting, TLS, metrics and tracing.

## Assumptions

- Currencies: GBP, USD and EUR. The brief asks for at most three ISO codes
  without naming them.
- POST returns 201 for both Authorized and Declined, since a payment resource
  was created either way.
- Routes keep the template's `/api/payments` prefix.
- `Processing` is only ever returned by GET. POST reports it only inside the
  502 timeout and 202 duplicate bodies.
- An in-flight duplicate returns 202. 409 would also be reasonable.
- A 400 from the bank is treated as `bank.ErrUnavailable`, since in either
  case the bank did not process the payment.

## Next steps

- Postgres behind the existing repository interface, with the idempotency
  key as a unique index.
- Reconciliation of `Processing` payments against the bank.
- Structured logging with request IDs, and metrics around the bank call.
- Require the `Idempotency-Key` header.
