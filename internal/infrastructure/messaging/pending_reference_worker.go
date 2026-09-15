package messaging

import (
	"context"
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
		if err != nil || ref == nil || ref.Status() != domain.StatusProcessed {
			// Referência ainda não disponível
			continue
		}

		// Referência agora encontrada! Executa a resolução definitiva
		w.logger.Info(
			"Referência encontrada pelo worker! Resolvendo transação",
			"transactionId", tx.ID(),
			"referenceId", ref.ID(),
		)
		_ = w.resolveTransaction(ctx, tx, ref, now)
	}
}

func (w *PendingReferenceWorker) rejectExpired(ctx context.Context, tx *domain.WagerTransaction, now time.Time) error {
	dbTx, err := w.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer dbTx.Rollback(ctx)

	_ = tx.TransitionToRejected("REFERENCE_NOT_FOUND", now)
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
			FailureCode:           "REFERENCE_NOT_FOUND",
		},
	)
	if err := w.repo.CreateOutboxEvent(ctx, dbTx, evRej); err != nil {
		return err
	}
	return dbTx.Commit(ctx)
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
			_ = tx.TransitionToRejected("ROLLBACK_INSUFFICIENT_FUNDS", now)
			_ = w.repo.UpdateTransaction(ctx, dbTx, tx)
			return dbTx.Commit(ctx)
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
	_ = w.repo.CreateOutboxEvent(ctx, dbTx, evProcessed)

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
	_ = w.repo.CreateOutboxEvent(ctx, dbTx, evBalance)

	return dbTx.Commit(ctx)
}

func s_repoCreateLedgerEntry(ctx context.Context, dbTx pgx.Tx, repo *database.Repository, entry *domain.WalletLedgerEntry) error {
	return repo.CreateLedgerEntry(ctx, dbTx, entry)
}
