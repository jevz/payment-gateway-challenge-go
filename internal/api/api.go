package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/bank"
	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/handlers"
	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/payment"
	"github.com/cko-recruitment/payment-gateway-challenge-go/internal/repository"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/sync/errgroup"
)

// bankCallTimeout is the deadline for each call to the bank.
const bankCallTimeout = 3 * time.Second

type Api struct {
	router   *chi.Mux
	payments *handlers.PaymentsHandler
}

// New builds the repository, bank client, service and handlers, and sets up
// the routes.
func New(bankBaseURL string) *Api {
	a := &Api{}
	repo := repository.NewPaymentsRepository()
	bankClient := bank.NewClient(bankBaseURL, bankCallTimeout)
	a.payments = handlers.NewPaymentsHandler(payment.NewService(bankClient, repo))
	a.setupRouter()

	return a
}

// Handler returns the router. Used by the e2e tests.
func (a *Api) Handler() http.Handler {
	return a.router
}

func (a *Api) Run(ctx context.Context, addr string) error {
	httpServer := &http.Server{
		Addr:        addr,
		Handler:     a.router,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		<-ctx.Done()
		fmt.Printf("shutting down HTTP server\n")
		return httpServer.Shutdown(ctx)
	})

	g.Go(func() error {
		fmt.Printf("starting HTTP server on %s\n", addr)
		err := httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			return err
		}

		return nil
	})

	return g.Wait()
}

func (a *Api) setupRouter() {
	a.router = chi.NewRouter()
	a.router.Use(middleware.Logger)

	a.router.Get("/ping", a.PingHandler())
	a.router.Get("/swagger/*", a.SwaggerHandler())

	a.router.Post("/api/payments", a.PostPaymentHandler())
	a.router.Get("/api/payments/{id}", a.GetPaymentHandler())
}
