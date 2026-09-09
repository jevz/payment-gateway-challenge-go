# Demo

## Prerequisites
```
docker compose up -d
go run .
```


## Paste the following lines into a terminal

```bash
pay()  { printf '{"card_number":"%s","expiry_month":4,"expiry_year":2030,"currency":"GBP","amount":%s,"cvv":"123"}' "$1" "${2:-100}"; }
post() { curl -s -o /tmp/resp.json -w 'HTTP %{http_code}\n' -X POST localhost:8090/api/payments -H 'Content-Type: application/json' "$@"; jq . /tmp/resp.json; }
get()  { curl -s -o /tmp/resp.json -w 'HTTP %{http_code}\n' localhost:8090/api/payments/"$1"; jq . /tmp/resp.json; }
```


## Then each of these lines is a different scenario test

```bash
post -d "$(pay 2222405343248877)"          # 201 Authorized (odd last digit)
get <paste the id from above>              # 200, same payment back
post -d "$(pay 2222405343248872)"          # 201 Declined (even last digit)
post -d "$(pay 2222405343248870)"          # 502 bank_unavailable, retryable true (ends in 0)

post -d '{"card_number":"12ab","expiry_month":13,"expiry_year":2020,"currency":"POUNDS","amount":0,"cvv":"12345"}'   # 400 rejected, five field errors listed

post -H 'Idempotency-Key: k1' -d "$(pay 2222405343248877)"      # 201
post -H 'Idempotency-Key: k1' -d "$(pay 2222405343248877)"      # 201, identical id, bank not called again
post -H 'Idempotency-Key: k1' -d "$(pay 2222405343248877 999)"  # 422 idempotency_key_reuse

get 123                                   # 404 not_found
```


## Not shown here because they need concurrency or a stalled bank, both covered by tests

- 202 in-flight duplicate — see `TestDuplicateWhileInFlightGetsProcessingNotSecondBankCall` and `TestConcurrentDuplicatesCauseExactlyOneBankCall` in `internal/payment/service_test.go`
- 502 timeout with the payment kept as `Processing` — see `TestBankTimeoutLeavesPaymentProcessing` in `internal/payment/service_test.go` and `TestPostPaymentBankTimeoutLeavesPaymentRetrievable` in `internal/handlers/payments_handler_test.go`

