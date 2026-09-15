package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"backend-challenge-go/internal/application"
	"backend-challenge-go/internal/domain"
	"backend-challenge-go/internal/infrastructure/auth"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	walletService         *application.WalletService
	wagerService          *application.WagerService
	reconciliationService *application.ReconciliationService
	pgPool                *pgxpool.Pool
}

func NewHandler(
	walletService *application.WalletService,
	wagerService *application.WagerService,
	reconciliationService *application.ReconciliationService,
	pgPool *pgxpool.Pool,
) *Handler {
	return &Handler{
		walletService:         walletService,
		wagerService:          wagerService,
		reconciliationService: reconciliationService,
		pgPool:                pgPool,
	}
}

// =========================================================================
// HEALTH CHECKS
// =========================================================================

func (h *Handler) Liveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "UP"})
}

func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.pgPool.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "DOWN",
			"error":  "postgres unreachable: " + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "UP",
		"database": "CONNECTED",
	})
}

// =========================================================================
// WALLETS
// =========================================================================

func (h *Handler) CreateWallet(w http.ResponseWriter, r *http.Request) {
	var dto application.CreateWalletDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	res, err := h.walletService.CreateWallet(r.Context(), dto)
	if err != nil {
		if errors.Is(err, domain.ErrDuplicateWallet) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, domain.ErrCurrencyMismatch) || errors.Is(err, domain.ErrNegativeBalance) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, res)
}

func (h *Handler) GetWallet(w http.ResponseWriter, r *http.Request) {
	walletId := chi.URLParam(r, "walletId")
	res, err := h.walletService.GetWallet(r.Context(), walletId)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) GetLedger(w http.ResponseWriter, r *http.Request) {
	walletId := chi.URLParam(r, "walletId")
	cursor := r.URL.Query().Get("cursor")
	limitStr := r.URL.Query().Get("limit")

	limit := 50
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	res, err := h.walletService.GetLedger(r.Context(), walletId, cursor, limit)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

// =========================================================================
// WAGERING TRANSACTIONS
// =========================================================================

func (h *Handler) ProcessTransaction(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "missing mandatory 'Idempotency-Key' header")
		return
	}

	var req application.WagerRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.IdempotencyKey = idempotencyKey

	// Isolamento de Provedores: valida se o token pertence ao providerId solicitado
	callerProvider := auth.GetProviderID(r.Context())
	if callerProvider != "" && callerProvider != req.ProviderID && callerProvider != "admin" {
		writeError(w, http.StatusForbidden, "forbidden: provider token does not match requested providerId")
		return
	}

	res, err := h.wagerService.ProcessTransaction(r.Context(), req)
	if err != nil {
		if errors.Is(err, domain.ErrIdempotencyConflict) {
			writeError(w, http.StatusConflict, "conflito: chave de idempotencia reutilizada com payload divergente")
			return
		}
		if errors.Is(err, domain.ErrWalletNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, domain.ErrCurrencyMismatch) ||
			errors.Is(err, domain.ErrInvalidTransactionKind) ||
			errors.Is(err, domain.ErrReferenceNotFound) ||
			errors.Is(err, domain.ErrReferenceMismatch) ||
			errors.Is(err, domain.ErrOpeningNotAllowedExternally) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) GetTransactionByID(w http.ResponseWriter, r *http.Request) {
	transactionId := chi.URLParam(r, "transactionId")
	tx, err := h.wagerService.GetTransactionByID(r.Context(), transactionId)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tx == nil {
		writeError(w, http.StatusNotFound, "transação não encontrada")
		return
	}

	// Validação de isolamento do provedor
	callerProvider := auth.GetProviderID(r.Context())
	if callerProvider != "" && callerProvider != tx.ProviderID() && callerProvider != "admin" && !tx.IsInternal() {
		writeError(w, http.StatusForbidden, "forbidden: transação pertence a outro provedor")
		return
	}

	writeJSON(w, http.StatusOK, mapTransaction(tx))
}

func (h *Handler) GetTransactionByExternalID(w http.ResponseWriter, r *http.Request) {
	providerId := chi.URLParam(r, "providerId")
	externalTransactionId := chi.URLParam(r, "externalTransactionId")

	callerProvider := auth.GetProviderID(r.Context())
	if callerProvider != "" && callerProvider != providerId && callerProvider != "admin" {
		writeError(w, http.StatusForbidden, "forbidden: provider não autorizado a consultar dados de outro provedor")
		return
	}

	tx, err := h.wagerService.GetTransactionByExternalID(r.Context(), providerId, externalTransactionId)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tx == nil {
		writeError(w, http.StatusNotFound, "transação não encontrada")
		return
	}

	writeJSON(w, http.StatusOK, mapTransaction(tx))
}

// =========================================================================
// RECONCILIAÇÃO
// =========================================================================

func (h *Handler) ReconcileWallet(w http.ResponseWriter, r *http.Request) {
	walletId := chi.URLParam(r, "walletId")
	res, err := h.reconciliationService.ReconcileWallet(r.Context(), walletId)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, res)
}

// Helpers
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func mapTransaction(t *domain.WagerTransaction) map[string]interface{} {
	m := map[string]interface{}{
		"id":                    t.ID(),
		"providerId":            t.ProviderID(),
		"externalTransactionId": t.ExternalTransactionID(),
		"idempotencyKey":        t.IdempotencyKey(),
		"walletId":              t.WalletID(),
		"playerId":              t.PlayerID(),
		"roundId":               t.RoundID(),
		"gameId":                t.GameID(),
		"kind":                  t.Kind(),
		"money":                 t.Money(),
		"status":                t.Status(),
		"createdAt":             t.CreatedAt(),
	}
	if t.FailureCode() != "" {
		m["failureCode"] = t.FailureCode()
	}
	if !t.BalanceAfter().IsZero() {
		m["balanceAfter"] = t.BalanceAfter()
	}
	if t.ReferenceExternalTransactionID() != "" {
		m["referenceExternalTransactionId"] = t.ReferenceExternalTransactionID()
	}
	return m
}
