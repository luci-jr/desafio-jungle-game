package messaging

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"backend-challenge-go/internal/domain"
	"backend-challenge-go/internal/infrastructure/database"
	"github.com/jackc/pgx/v5"
)

// PendingReferenceWorker resolve operações de REFUND ou ROLLBACK que chegaram antes da referência.
type PendingReferenceWorker struct {
	repo   *database.Repository
	logger *slog.Logger
}

func NewPendingReferenceWorker(repo *database.Repository, logger *slog.Logger) *PendingReferenceWorker {
	return &PendingReferenceWorker{
		repo:   repo,
		logger: logger,
	}
}

// Start inicia o ciclo de verificação de referências pendentes.
func (w *PendingReferenceWorker) Start(ctx context.Context) {
	w.logger.Info("Iniciando PendingReferenceWorker (Resolução de Reversões Fora de Ordem)")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Encerrando PendingReferenceWorker graciosamente...")
			return
		case <-ticker.C:
			w.checkPendingReferences(ctx)
		}
	}
}

func (w *PendingReferenceWorker) checkPendingReferences(ctx context.Context) {
	pendingList, err := w.repo.GetPendingReferenceTransactions(ctx, 10)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("Erro ao buscar transações PENDING_REFERENCE", "error", err)
		}
		return
	}

	now := time.Now().UTC()
	for _, tx := range pendingList {
		// Se passou de 1 minuto em PENDING_REFERENCE (TTL expirado) -> Finaliza como REJECTED
		if now.Sub(tx.CreatedAt()) > 1*time.Minute {
			w.logger.Warn(
				"TTL expirado para transação aguardando referência. Finalizando como REJECTED",
				"transactionId", tx.ID(),
				"refExternalId", tx.ReferenceExternalTransactionID(),
			)
			_ = w.rejectExpired(ctx, tx, now)
			continue
		}

		// Tenta buscar a transação de referência no banco
		ref, err := w.repo.GetTransactionByExternalID(ctx, tx.ProviderID(), tx.ReferenceExternalTransactionID())
		if err != nil {
			w.logger.Error("Erro ao buscar referência pendente", "transactionId", tx.ID(), "error", err)
			continue
		}
		if ref == nil || !ref.IsTerminal() {
			// A referência ainda não chegou ou também está em processamento.
			continue
		}
		if err := w.validateReference(ctx, tx, ref); err != nil {
			failureCode := pendingReferenceFailureCode(err)
			w.logger.Warn("Referência pendente rejeitada por regra de negócio", "transactionId", tx.ID(), "failureCode", failureCode, "error", err)
			if rejectErr := w.reject(ctx, tx, failureCode, now); rejectErr != nil {
				w.logger.Error("Erro ao rejeitar referência pendente", "transactionId", tx.ID(), "error", rejectErr)
			}
			continue
		}

		// Referência agora encontrada! Executa a resolução definitiva
		w.logger.Info(
			"Referência encontrada pelo worker! Resolvendo transação",
			"transactionId", tx.ID(),
			"referenceId", ref.ID(),
		)
		if err := w.resolveTransaction(ctx, tx, ref, now); err != nil {
			w.logger.Error("Erro ao resolver referência pendente", "transactionId", tx.ID(), "error", err)
		}
	}
}

func (w *PendingReferenceWorker) rejectExpired(ctx context.Context, tx *domain.WagerTransaction, now time.Time) error {
	return w.reject(ctx, tx, "REFERENCE_NOT_FOUND", now)
}

// reject finaliza uma pendência e registra o evento de rejeição no mesmo commit.
func (w *PendingReferenceWorker) reject(ctx context.Context, tx *domain.WagerTransaction, failureCode string, now time.Time) error {
	dbTx, err := w.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer dbTx.Rollback(ctx)

	if err := tx.TransitionToRejected(failureCode, now); err != nil {
		return err
	}
	if err := w.repo.UpdateTransaction(ctx, dbTx, tx); err != nil {
		return err
	}

	evRej := domain.NewEventEnvelope(
		domain.EventTypeWagerTransactionRejected,
		tx.WalletID(),
		tx.ID(),
		tx.ID(),
		now,
		domain.PayloadWagerTransactionRejected{
			TransactionID:         tx.ID(),
			ProviderID:            tx.ProviderID(),
			ExternalTransactionID: tx.ExternalTransactionID(),
			WalletID:              tx.WalletID(),
			PlayerID:              tx.PlayerID(),
			Kind:                  string(tx.Kind()),
			Amount:                tx.Money().AmountString(),
			Currency:              tx.Money().Currency(),
			FailureCode:           failureCode,
		},
	)
	if err := w.repo.CreateOutboxEvent(ctx, dbTx, evRej); err != nil {
		return err
	}
	return dbTx.Commit(ctx)
}

// validateReference repete, no fluxo assíncrono, as mesmas regras usadas quando
// uma reversão encontra sua referência imediatamente.
func (w *PendingReferenceWorker) validateReference(ctx context.Context, tx, ref *domain.WagerTransaction) error {
	if ref.Status() != domain.StatusProcessed {
		return domain.ErrReferenceNotProcessed
	}

	if err := validateReferenceFields(tx, ref); err != nil {
		return err
	}

	alreadyReversed, err := w.repo.HasSuccessfulReversal(ctx, tx.ProviderID(), tx.ReferenceExternalTransactionID())
	if err != nil {
		return err
	}
	if alreadyReversed {
		return domain.ErrReferenceAlreadyReversed
	}
	return nil
}

// validateReferenceFields contém as regras puras compartilhadas pela resolução
// assíncrona; a verificação de reversão prévia continua persistente no banco.
func validateReferenceFields(tx, ref *domain.WagerTransaction) error {
	validKind := (tx.Kind() == domain.KindRefund && ref.Kind() == domain.KindBet) ||
		(tx.Kind() == domain.KindRollback && (ref.Kind() == domain.KindBet || ref.Kind() == domain.KindWin || ref.Kind() == domain.KindRefund))
	if !validKind ||
		ref.ProviderID() != tx.ProviderID() ||
		ref.PlayerID() != tx.PlayerID() ||
		ref.WalletID() != tx.WalletID() ||
		ref.RoundID() != tx.RoundID() ||
		!ref.Money().Equal(tx.Money()) {
		return domain.ErrReferenceMismatch
	}
	return nil
}

func pendingReferenceFailureCode(err error) string {
	switch {
	case errors.Is(err, domain.ErrReferenceNotProcessed):
		return "REFERENCE_NOT_PROCESSED"
	case errors.Is(err, domain.ErrReferenceAlreadyReversed):
		return "REFERENCE_ALREADY_REVERSED"
	default:
		return "REFERENCE_MISMATCH"
	}
}

func (w *PendingReferenceWorker) resolveTransaction(
	ctx context.Context,
	tx *domain.WagerTransaction,
	ref *domain.WagerTransaction,
	now time.Time,
) error {
	dbTx, err := w.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer dbTx.Rollback(ctx)

	wallet, err := w.repo.GetWalletForUpdate(ctx, dbTx, tx.WalletID())
	if err != nil {
		return err
	}

	var entry *domain.WalletLedgerEntry
	if tx.Kind() == domain.KindRefund || (tx.Kind() == domain.KindRollback && ref.Kind() == domain.KindBet) {
		entry, err = wallet.Credit(tx.Money(), tx.ID(), now)
	} else {
		less, _ := wallet.Balance().LessThan(tx.Money())
		if less {
			return w.rejectInTx(ctx, dbTx, tx, "ROLLBACK_INSUFFICIENT_FUNDS", now)
		}
		entry, err = wallet.Debit(tx.Money(), tx.ID(), now)
	}

	if err != nil {
		return err
	}

	if err := w.repo.UpdateWallet(ctx, dbTx, wallet); err != nil {
		return err
	}
	if err := s_repoCreateLedgerEntry(ctx, dbTx, w.repo, entry); err != nil {
		return err
	}

	tx.ResolveReference(ref.ID())
	_ = tx.TransitionToProcessed(wallet.Balance(), now)
	if err := w.repo.UpdateTransaction(ctx, dbTx, tx); err != nil {
		return err
	}

	// Emite eventos de sucesso no outbox
	evProcessed := domain.NewEventEnvelope(
		domain.EventTypeWagerTransactionProcessed,
		wallet.ID(),
		tx.ID(),
		tx.ID(),
		now,
		domain.PayloadWagerTransactionProcessed{
			TransactionID:         tx.ID(),
			ProviderID:            tx.ProviderID(),
			ExternalTransactionID: tx.ExternalTransactionID(),
			WalletID:              wallet.ID(),
			PlayerID:              tx.PlayerID(),
			Kind:                  string(tx.Kind()),
			Amount:                tx.Money().AmountString(),
			Currency:              tx.Money().Currency(),
			BalanceAfter:          wallet.Balance().AmountString(),
		},
	)
	if err := w.repo.CreateOutboxEvent(ctx, dbTx, evProcessed); err != nil {
		return err
	}

	evBalance := domain.NewEventEnvelope(
		domain.EventTypeWalletBalanceChanged,
		wallet.ID(),
		tx.ID(),
		tx.ID(),
		now,
		domain.PayloadWalletBalanceChanged{
			WalletID:      wallet.ID(),
			TransactionID: tx.ID(),
			Direction:     string(entry.Direction()),
			Amount:        entry.Amount().AmountString(),
			Currency:      entry.Amount().Currency(),
			BalanceBefore: entry.BalanceBefore().AmountString(),
			BalanceAfter:  entry.BalanceAfter().AmountString(),
			WalletVersion: wallet.Version(),
		},
	)
	if err := w.repo.CreateOutboxEvent(ctx, dbTx, evBalance); err != nil {
		return err
	}

	return dbTx.Commit(ctx)
}

// rejectInTx é a variante usada após o lock da carteira já ter sido adquirido.
func (w *PendingReferenceWorker) rejectInTx(ctx context.Context, dbTx pgx.Tx, tx *domain.WagerTransaction, failureCode string, now time.Time) error {
	if err := tx.TransitionToRejected(failureCode, now); err != nil {
		return err
	}
	if err := w.repo.UpdateTransaction(ctx, dbTx, tx); err != nil {
		return err
	}
	ev := domain.NewEventEnvelope(
		domain.EventTypeWagerTransactionRejected, tx.WalletID(), tx.ID(), tx.ID(), now,
		domain.PayloadWagerTransactionRejected{
			TransactionID: tx.ID(), ProviderID: tx.ProviderID(), ExternalTransactionID: tx.ExternalTransactionID(),
			WalletID: tx.WalletID(), PlayerID: tx.PlayerID(), Kind: string(tx.Kind()),
			Amount: tx.Money().AmountString(), Currency: tx.Money().Currency(), FailureCode: failureCode,
		},
	)
	if err := w.repo.CreateOutboxEvent(ctx, dbTx, ev); err != nil {
		return err
	}
	return dbTx.Commit(ctx)
}

func s_repoCreateLedgerEntry(ctx context.Context, dbTx pgx.Tx, repo *database.Repository, entry *domain.WalletLedgerEntry) error {
	return repo.CreateLedgerEntry(ctx, dbTx, entry)
}
