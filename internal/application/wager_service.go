package application

import (
	"context"
	"fmt"
	"time"

	"backend-challenge-go/internal/domain"
	"backend-challenge-go/internal/infrastructure/database"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// WagerService orquestra o processamento financeiro distribuído de apostas.
type WagerService struct {
	repo *database.Repository
}

func NewWagerService(repo *database.Repository) *WagerService {
	return &WagerService{repo: repo}
}

// WagerRequestDTO é a entrada unificada para transações externas (via HTTP e SQS).
type WagerRequestDTO struct {
	ProviderID                     string                 `json:"providerId"`
	ExternalTransactionID          string                 `json:"externalTransactionId"`
	IdempotencyKey                 string                 `json:"idempotencyKey"`
	PlayerID                       string                 `json:"playerId"`
	WalletID                       string                 `json:"walletId"`
	RoundID                        string                 `json:"roundId"`
	GameID                         string                 `json:"gameId"`
	Kind                           domain.TransactionKind `json:"kind"`
	Money                          domain.Money           `json:"money"`
	ReferenceExternalTransactionID string                 `json:"referenceExternalTransactionId,omitempty"`
}

// WagerResponseDTO é a resposta oficial do contrato após processamento.
type WagerResponseDTO struct {
	TransactionID    string        `json:"transactionId"`
	Status           string        `json:"status"`
	Balance          *domain.Money `json:"balance,omitempty"`
	FailureCode      string        `json:"failureCode,omitempty"`
	IdempotentReplay bool          `json:"idempotentReplay"`
}

// ProcessTransaction abre e confirma a transação financeira usada pela API HTTP.
func (s *WagerService) ProcessTransaction(ctx context.Context, req WagerRequestDTO) (*WagerResponseDTO, error) {
	dbTx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("falha ao iniciar transação: %w", err)
	}
	defer dbTx.Rollback(ctx)

	response, err := s.ProcessTransactionInTx(ctx, dbTx, req)
	if err != nil {
		return nil, err
	}

	if err := dbTx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("erro ao comitar transação financeira: %w", err)
	}

	return response, nil
}

// ProcessTransactionInTx processa a operação usando uma transação já aberta.
//
// Esse método permite que o consumidor SQS grave a Inbox junto com a carteira,
// o ledger, a transação financeira e a Outbox no mesmo commit.
func (s *WagerService) ProcessTransactionInTx(
	ctx context.Context,
	dbTx pgx.Tx,
	req WagerRequestDTO,
) (*WagerResponseDTO, error) {
	now := time.Now().UTC()

	// 1. Cálculo determinístico do Hash Canônico do payload de negócio
	canonicalHash, err := domain.ComputeCanonicalPayloadHash(domain.CanonicalPayloadDTO{
		ExternalTransactionId:          req.ExternalTransactionID,
		GameId:                         req.GameID,
		Kind:                           string(req.Kind),
		MoneyAmount:                    req.Money.AmountString(),
		MoneyCurrency:                  req.Money.Currency(),
		PlayerId:                       req.PlayerID,
		ProviderId:                     req.ProviderID,
		ReferenceExternalTransactionId: req.ReferenceExternalTransactionID,
		RoundId:                        req.RoundID,
		WalletId:                       req.WalletID,
	})
	if err != nil {
		return nil, fmt.Errorf("falha ao calcular hash canônico: %w", err)
	}

	// 2. Validação das regras de domínio da operação externa
	transactionID := uuid.NewString()
	txDomain, err := domain.NewExternalTransaction(
		transactionID,
		req.ProviderID,
		req.ExternalTransactionID,
		req.IdempotencyKey,
		req.WalletID,
		req.PlayerID,
		req.RoundID,
		req.GameID,
		req.Kind,
		req.Money,
		req.ReferenceExternalTransactionID,
		now,
	)
	if err != nil {
		return nil, err
	}

	// 3. Bloqueio pessimista (SELECT ... FOR UPDATE) na carteira envolvida
	wallet, err := s.repo.GetWalletForUpdate(ctx, dbTx, req.WalletID)
	if err != nil {
		return nil, err
	}

	// 3.1. Double-Check de Idempotência sob o Lock da Carteira:
	// Se outra transação concorrente da mesma carteira comitou enquanto aguardávamos o lock
	existingInTx, err := s.repo.GetTransactionByIdempotencyKeyTx(ctx, dbTx, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if existingInTx == nil {
		existingInTx, err = s.repo.GetTransactionByExternalIDTx(ctx, dbTx, req.ProviderID, req.ExternalTransactionID)
		if err != nil {
			return nil, err
		}
	}
	if existingInTx != nil {
		if existingInTx.PayloadHash() != canonicalHash ||
			existingInTx.IdempotencyKey() != req.IdempotencyKey ||
			existingInTx.ExternalTransactionID() != req.ExternalTransactionID {
			return nil, domain.ErrIdempotencyConflict
		}
		balance := existingInTx.BalanceAfter()
		return &WagerResponseDTO{
			TransactionID:    existingInTx.ID(),
			Status:           string(existingInTx.Status()),
			Balance:          &balance,
			FailureCode:      existingInTx.FailureCode(),
			IdempotentReplay: true,
		}, nil
	}

	if wallet.Currency() != req.Money.Currency() {
		return nil, domain.ErrCurrencyMismatch
	}

	// 5. Execução de acordo com o Tipo de Operação
	switch req.Kind {
	case domain.KindBet:
		less, err := wallet.Balance().LessThan(req.Money)
		if err != nil {
			return nil, err
		}

		// Caso de saldo insuficiente: rejeita a operação com código estável INSUFFICIENT_FUNDS
		if less {
			_ = txDomain.TransitionToRejected("INSUFFICIENT_FUNDS", now)
			if err := s.repo.CreateTransaction(ctx, dbTx, txDomain); err != nil {
				return nil, err
			}

			// Publica evento de rejeição na outbox
			evRej := domain.NewEventEnvelope(
				domain.EventTypeWagerTransactionRejected,
				wallet.ID(),
				transactionID,
				transactionID,
				now,
				domain.PayloadWagerTransactionRejected{
					TransactionID:         transactionID,
					ProviderID:            req.ProviderID,
					ExternalTransactionID: req.ExternalTransactionID,
					WalletID:              req.WalletID,
					PlayerID:              req.PlayerID,
					Kind:                  string(domain.KindBet),
					Amount:                req.Money.AmountString(),
					Currency:              req.Money.Currency(),
					FailureCode:           "INSUFFICIENT_FUNDS",
				},
			)
			if err := s.repo.CreateOutboxEvent(ctx, dbTx, evRej); err != nil {
				return nil, err
			}

			currentBal := wallet.Balance()
			return &WagerResponseDTO{
				TransactionID:    transactionID,
				Status:           string(domain.StatusRejected),
				Balance:          &currentBal,
				FailureCode:      "INSUFFICIENT_FUNDS",
				IdempotentReplay: false,
			}, nil
		}

		// Saldo suficiente: efetua o débito
		entry, err := wallet.Debit(req.Money, transactionID, now)
		if err != nil {
			return nil, err
		}

		_ = txDomain.TransitionToProcessed(wallet.Balance(), now)
		if err := s.repo.CreateTransaction(ctx, dbTx, txDomain); err != nil {
			return nil, err
		}
		if err := s.repo.UpdateWallet(ctx, dbTx, wallet); err != nil {
			return nil, err
		}
		if err := s.repo.CreateLedgerEntry(ctx, dbTx, entry); err != nil {
			return nil, err
		}

		if err := s.emitProcessedEvents(ctx, dbTx, txDomain, wallet, entry, now); err != nil {
			return nil, err
		}

	case domain.KindWin:
		entry, err := wallet.Credit(req.Money, transactionID, now)
		if err != nil {
			return nil, err
		}

		_ = txDomain.TransitionToProcessed(wallet.Balance(), now)
		if err := s.repo.CreateTransaction(ctx, dbTx, txDomain); err != nil {
			return nil, err
		}
		if err := s.repo.UpdateWallet(ctx, dbTx, wallet); err != nil {
			return nil, err
		}
		if err := s.repo.CreateLedgerEntry(ctx, dbTx, entry); err != nil {
			return nil, err
		}

		if err := s.emitProcessedEvents(ctx, dbTx, txDomain, wallet, entry, now); err != nil {
			return nil, err
		}

	case domain.KindLoss:
		// LOSS exige 0.00, não altera carteira nem cria ledger
		_ = txDomain.TransitionToProcessed(wallet.Balance(), now)
		if err := s.repo.CreateTransaction(ctx, dbTx, txDomain); err != nil {
			return nil, err
		}

		// Produz apenas WagerTransactionProcessed (sem WalletBalanceChanged)
		evProcessed := domain.NewEventEnvelope(
			domain.EventTypeWagerTransactionProcessed,
			wallet.ID(),
			transactionID,
			transactionID,
			now,
			domain.PayloadWagerTransactionProcessed{
				TransactionID:         transactionID,
				ProviderID:            req.ProviderID,
				ExternalTransactionID: req.ExternalTransactionID,
				WalletID:              req.WalletID,
				PlayerID:              req.PlayerID,
				Kind:                  string(domain.KindLoss),
				Amount:                req.Money.AmountString(),
				Currency:              req.Money.Currency(),
				BalanceAfter:          wallet.Balance().AmountString(),
			},
		)
		if err := s.repo.CreateOutboxEvent(ctx, dbTx, evProcessed); err != nil {
			return nil, err
		}

	case domain.KindRefund:
		ref, err := s.repo.GetTransactionByExternalID(ctx, req.ProviderID, req.ReferenceExternalTransactionID)
		if err != nil {
			return nil, err
		}

		// Se a referência ainda não chegou -> PENDING_REFERENCE
		if ref == nil {
			_ = txDomain.TransitionToPendingReference(now)
			if err := s.repo.CreateTransaction(ctx, dbTx, txDomain); err != nil {
				return nil, err
			}

			evPending := domain.NewEventEnvelope(
				domain.EventTypeWagerTransactionPendingReference,
				wallet.ID(),
				transactionID,
				transactionID,
				now,
				domain.PayloadWagerTransactionPendingReference{
					TransactionID:                  transactionID,
					ProviderID:                     req.ProviderID,
					ExternalTransactionID:          req.ExternalTransactionID,
					WalletID:                       req.WalletID,
					ReferenceExternalTransactionID: req.ReferenceExternalTransactionID,
				},
			)
			if err := s.repo.CreateOutboxEvent(ctx, dbTx, evPending); err != nil {
				return nil, err
			}

			bal := wallet.Balance()
			return &WagerResponseDTO{
				TransactionID:    transactionID,
				Status:           string(domain.StatusPendingReference),
				Balance:          &bal,
				IdempotentReplay: false,
			}, nil
		}

		if err := s.validateReversalReference(ctx, req, ref, domain.KindBet); err != nil {
			return nil, err
		}

		// Devolve o débito da aposta como crédito
		entry, err := wallet.Credit(req.Money, transactionID, now)
		if err != nil {
			return nil, err
		}

		txDomain.ResolveReference(ref.ID())
		_ = txDomain.TransitionToProcessed(wallet.Balance(), now)
		if err := s.repo.CreateTransaction(ctx, dbTx, txDomain); err != nil {
			return nil, err
		}
		if err := s.repo.UpdateWallet(ctx, dbTx, wallet); err != nil {
			return nil, err
		}
		if err := s.repo.CreateLedgerEntry(ctx, dbTx, entry); err != nil {
			return nil, err
		}

		if err := s.emitProcessedEvents(ctx, dbTx, txDomain, wallet, entry, now); err != nil {
			return nil, err
		}

	case domain.KindRollback:
		ref, err := s.repo.GetTransactionByExternalID(ctx, req.ProviderID, req.ReferenceExternalTransactionID)
		if err != nil {
			return nil, err
		}

		// Se a referência ainda não chegou -> PENDING_REFERENCE
		if ref == nil {
			_ = txDomain.TransitionToPendingReference(now)
			if err := s.repo.CreateTransaction(ctx, dbTx, txDomain); err != nil {
				return nil, err
			}

			evPending := domain.NewEventEnvelope(
				domain.EventTypeWagerTransactionPendingReference,
				wallet.ID(),
				transactionID,
				transactionID,
				now,
				domain.PayloadWagerTransactionPendingReference{
					TransactionID:                  transactionID,
					ProviderID:                     req.ProviderID,
					ExternalTransactionID:          req.ExternalTransactionID,
					WalletID:                       req.WalletID,
					ReferenceExternalTransactionID: req.ReferenceExternalTransactionID,
				},
			)
			if err := s.repo.CreateOutboxEvent(ctx, dbTx, evPending); err != nil {
				return nil, err
			}

			bal := wallet.Balance()
			return &WagerResponseDTO{
				TransactionID:    transactionID,
				Status:           string(domain.StatusPendingReference),
				Balance:          &bal,
				IdempotentReplay: false,
			}, nil
		}

		if err := s.validateReversalReference(
			ctx,
			req,
			ref,
			domain.KindBet,
			domain.KindWin,
			domain.KindRefund,
		); err != nil {
			return nil, err
		}

		// Desfaz o movimento original
		var entry *domain.WalletLedgerEntry
		if ref.Kind() == domain.KindBet {
			// Desfazer aposta = creditar valor
			entry, err = wallet.Credit(req.Money, transactionID, now)
		} else if ref.Kind() == domain.KindWin || ref.Kind() == domain.KindRefund {
			// Desfazer ganho/reembolso = debitar valor
			less, _ := wallet.Balance().LessThan(req.Money)
			if less {
				_ = txDomain.TransitionToRejected("ROLLBACK_INSUFFICIENT_FUNDS", now)
				if err := s.repo.CreateTransaction(ctx, dbTx, txDomain); err != nil {
					return nil, err
				}
				evRej := domain.NewEventEnvelope(
					domain.EventTypeWagerTransactionRejected,
					wallet.ID(), transactionID, transactionID, now,
					domain.PayloadWagerTransactionRejected{
						TransactionID: transactionID, ProviderID: req.ProviderID,
						ExternalTransactionID: req.ExternalTransactionID, WalletID: req.WalletID,
						PlayerID: req.PlayerID, Kind: string(domain.KindRollback),
						Amount: req.Money.AmountString(), Currency: req.Money.Currency(),
						FailureCode: "ROLLBACK_INSUFFICIENT_FUNDS",
					},
				)
				if err := s.repo.CreateOutboxEvent(ctx, dbTx, evRej); err != nil {
					return nil, err
				}
				bal := wallet.Balance()
				return &WagerResponseDTO{
					TransactionID:    transactionID,
					Status:           string(domain.StatusRejected),
					Balance:          &bal,
					FailureCode:      "ROLLBACK_INSUFFICIENT_FUNDS",
					IdempotentReplay: false,
				}, nil
			}
			entry, err = wallet.Debit(req.Money, transactionID, now)
		} else {
			return nil, domain.ErrInvalidTransactionKind
		}

		if err != nil {
			return nil, err
		}

		txDomain.ResolveReference(ref.ID())
		_ = txDomain.TransitionToProcessed(wallet.Balance(), now)
		if err := s.repo.CreateTransaction(ctx, dbTx, txDomain); err != nil {
			return nil, err
		}
		if err := s.repo.UpdateWallet(ctx, dbTx, wallet); err != nil {
			return nil, err
		}
		if err := s.repo.CreateLedgerEntry(ctx, dbTx, entry); err != nil {
			return nil, err
		}

		if err := s.emitProcessedEvents(ctx, dbTx, txDomain, wallet, entry, now); err != nil {
			return nil, err
		}

	default:
		return nil, domain.ErrInvalidTransactionKind
	}

	finalBalance := wallet.Balance()
	return &WagerResponseDTO{
		TransactionID:    transactionID,
		Status:           string(txDomain.Status()),
		Balance:          &finalBalance,
		IdempotentReplay: false,
	}, nil
}

// validateReversalReference valida a referência antes de qualquer alteração
// de saldo. A regra também bloqueia uma segunda reversão bem-sucedida.
func (s *WagerService) validateReversalReference(
	ctx context.Context,
	req WagerRequestDTO,
	ref *domain.WagerTransaction,
	allowedKinds ...domain.TransactionKind,
) error {
	if ref.Status() != domain.StatusProcessed {
		return domain.ErrReferenceNotProcessed
	}

	allowed := false
	for _, kind := range allowedKinds {
		if ref.Kind() == kind {
			allowed = true
			break
		}
	}
	if !allowed ||
		ref.ProviderID() != req.ProviderID ||
		ref.PlayerID() != req.PlayerID ||
		ref.WalletID() != req.WalletID ||
		ref.RoundID() != req.RoundID ||
		!ref.Money().Equal(req.Money) {
		return domain.ErrReferenceMismatch
	}

	alreadyReversed, err := s.repo.HasSuccessfulReversal(
		ctx,
		req.ProviderID,
		req.ReferenceExternalTransactionID,
	)
	if err != nil {
		return err
	}
	if alreadyReversed {
		return domain.ErrReferenceAlreadyReversed
	}

	return nil
}

// emitProcessedEvents grava atomicamente os eventos de sucesso no outbox.
func (s *WagerService) emitProcessedEvents(
	ctx context.Context,
	tx pgx.Tx,
	t *domain.WagerTransaction,
	w *domain.Wallet,
	entry *domain.WalletLedgerEntry,
	now time.Time,
) error {
	evProcessed := domain.NewEventEnvelope(
		domain.EventTypeWagerTransactionProcessed,
		w.ID(),
		t.ID(),
		t.ID(),
		now,
		domain.PayloadWagerTransactionProcessed{
			TransactionID:         t.ID(),
			ProviderID:            t.ProviderID(),
			ExternalTransactionID: t.ExternalTransactionID(),
			WalletID:              w.ID(),
			PlayerID:              t.PlayerID(),
			Kind:                  string(t.Kind()),
			Amount:                t.Money().AmountString(),
			Currency:              t.Money().Currency(),
			BalanceAfter:          w.Balance().AmountString(),
		},
	)
	if err := s.repo.CreateOutboxEvent(ctx, tx, evProcessed); err != nil {
		return err
	}

	evBalance := domain.NewEventEnvelope(
		domain.EventTypeWalletBalanceChanged,
		w.ID(),
		t.ID(),
		t.ID(),
		now,
		domain.PayloadWalletBalanceChanged{
			WalletID:      w.ID(),
			TransactionID: t.ID(),
			Direction:     string(entry.Direction()),
			Amount:        entry.Amount().AmountString(),
			Currency:      entry.Amount().Currency(),
			BalanceBefore: entry.BalanceBefore().AmountString(),
			BalanceAfter:  entry.BalanceAfter().AmountString(),
			WalletVersion: w.Version(),
		},
	)
	return s.repo.CreateOutboxEvent(ctx, tx, evBalance)
}

// GetTransactionByID busca os detalhes de uma transação por ID interno.
func (s *WagerService) GetTransactionByID(ctx context.Context, id string) (*domain.WagerTransaction, error) {
	return s.repo.GetTransactionByID(ctx, id)
}

// GetTransactionByExternalID busca os detalhes pela chave externa do provedor.
func (s *WagerService) GetTransactionByExternalID(ctx context.Context, providerId, externalId string) (*domain.WagerTransaction, error) {
	return s.repo.GetTransactionByExternalID(ctx, providerId, externalId)
}
