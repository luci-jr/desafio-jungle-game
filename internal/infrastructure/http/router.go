package http

import (
	"log/slog"
	"net/http"
	"time"

	"backend-challenge-go/internal/infrastructure/auth"
	"backend-challenge-go/internal/infrastructure/config"
	"backend-challenge-go/internal/infrastructure/http/web"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter configura todas as rotas e middlewares HTTP.
func NewRouter(h *Handler, authValidator *auth.TokenValidator, cfg config.AppConfig, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(CORSMiddleware())
	r.Use(StructuredLoggerMiddleware(logger))

	// Rotas Públicas da Interface Visual Retro & Assets Embutidos
	web.RegisterWebRoutes(r, cfg.AuthIssuer, logger)

	// Rotas Públicas (Health Checks)
	r.Get("/health/live", h.Liveness)
	r.Get("/health/ready", h.Readiness)

	// Rotas Autenticadas via OIDC / Keycloak
	r.Group(func(protected chi.Router) {
		protected.Use(authValidator.MiddlewareHTTP)

		// Operações de carteira são internas e não ficam disponíveis para tokens
		// de provedores de jogos.
		protected.Group(func(internal chi.Router) {
			internal.Use(auth.RequireInternal)
			internal.Post("/wallets", h.CreateWallet)
			internal.Get("/wallets/{walletId}", h.GetWallet)
			internal.Get("/wallets/{walletId}/ledger", h.GetLedger)
			internal.Post("/wallets/{walletId}/reconciliation", h.ReconcileWallet)
		})

		// Transações Financeiras de Apostas
		protected.Post("/wagering/transactions", h.ProcessTransaction)
		protected.Get("/wagering/transactions/{transactionId}", h.GetTransactionByID)
		protected.Get("/providers/{providerId}/wagering/transactions/{externalTransactionId}", h.GetTransactionByExternalID)
	})

	return r
}

// CORSMiddleware permite requisições da interface web ou de outros clients locais.
func CORSMiddleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token, Idempotency-Key")
			w.Header().Set("Access-Control-Expose-Headers", "Link, Idempotency-Key")
			w.Header().Set("Access-Control-Max-Age", "300")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// StructuredLoggerMiddleware gera logs estruturados em formato JSON com tempo de resposta e status.
func StructuredLoggerMiddleware(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				logger.Info("Requisição HTTP",
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"duration_ms", time.Since(start).Milliseconds(),
					"req_id", middleware.GetReqID(r.Context()),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}
