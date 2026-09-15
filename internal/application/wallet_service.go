package application

import (
	"context"
	"fmt"
	"time"

	"backend-challenge-go/internal/domain"
	"backend-challenge-go/internal/infrastructure/database"
	"github.com/google/uuid"
)

// WalletService orquestra casos de uso de carteira e livro-razão.
type WalletService struct {
	repo *database.Repository
}

func NewWalletService(repo *database.Repository) *WalletService {
	return &WalletService{repo: repo}
}

// CreateWalletDTO é a entrada para criação de carteira.
type CreateWalletDTO struct {
	PlayerID       string       `json:"playerId"`
	InitialBalance domain.Money `json:"initialBalance"`
}

// WalletResponseDTO é o contrato retornado na criação e consulta de carteira.
type WalletResponseDTO struct {
	ID       string       `json:"id"`
	PlayerID string       `json:"playerId"`
	Balance  domain.Money `json:"balance"`
	Version  int64        `json:"version"`
}

// CreateWallet executa a abertura atômica de carteira com suporte ao lançamento OPENING.
func (s *WalletService) CreateWallet(ctx context.Context, dto CreateWalletDTO) (*WalletResponseDTO, error) {
	now := time.Now().UTC()
	walletID := uuid.NewString()

	wallet, entry, err := domain.NewWallet(
		walletID, dto.PlayerID, dto.InitialBalance.Currency(), dto.InitialBalance, now,
	)
	if err != nil {
		return nil, err
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("erro ao iniciar transação: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.repo.CreateWallet(ctx, tx, wallet); err != nil {
		return nil, err
	}

	// Se houver saldo inicial positivo, cria a transação interna OPENING, o ledger e o outbox
	if entry != nil {
		openingTx, err := domain.NewOpeningTransaction(walletID, walletID, dto.PlayerID, dto.InitialBalance, now)
		if err != nil {
			return nil, err
		}

		if err := s.repo.CreateTransaction(ctx, tx, openingTx); err != nil {
			return nil, err
		}

		if err := s.repo.CreateLedgerEntry(ctx, tx, entry); err != nil {
			return nil, err
		}

		// Evento WagerTransactionProcessed
		evProcessed := domain.NewEventEnvelope(
			domain.EventTypeWagerTransactionProcessed,
			walletID,
			walletID,
			walletID,
			now,
			domain.PayloadWagerTransactionProcessed{
				TransactionID: walletID,
				WalletID:      walletID,
				PlayerID:      dto.PlayerID,
				Kind:          string(domain.KindOpening),
				Amount:        dto.InitialBalance.AmountString(),
				Currency:      dto.InitialBalance.Currency(),
				BalanceAfter:  dto.InitialBalance.AmountString(),
			},
		)
		if err := s.repo.CreateOutboxEvent(ctx, tx, evProcessed); err != nil {
			return nil, err
		}

		// Evento WalletBalanceChanged
		zero, _ := domain.ZeroMoney(dto.InitialBalance.Currency())
		evBalance := domain.NewEventEnvelope(
			domain.EventTypeWalletBalanceChanged,
			walletID,
			walletID,
			walletID,
			now,
			domain.PayloadWalletBalanceChanged{
				WalletID:      walletID,
				TransactionID: walletID,
				Direction:     string(domain.DirectionCredit),
				Amount:        dto.InitialBalance.AmountString(),
				Currency:      dto.InitialBalance.Currency(),
				BalanceBefore: zero.AmountString(),
				BalanceAfter:  dto.InitialBalance.AmountString(),
				WalletVersion: 1,
			},
		)
		if err := s.repo.CreateOutboxEvent(ctx, tx, evBalance); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("erro ao comitar abertura de carteira: %w", err)
	}

	return &WalletResponseDTO{
		ID:       wallet.ID(),
		PlayerID: wallet.PlayerID(),
		Balance:  wallet.Balance(),
		Version:  wallet.Version(),
	}, nil
}

// GetWallet busca os dados da carteira por ID.
func (s *WalletService) GetWallet(ctx context.Context, walletId string) (*WalletResponseDTO, error) {
	w, err := s.repo.GetWallet(ctx, walletId)
	if err != nil {
		return nil, err
	}
	return &WalletResponseDTO{
		ID:       w.ID(),
		PlayerID: w.PlayerID(),
		Balance:  w.Balance(),
		Version:  w.Version(),
	}, nil
}

// LedgerPageDTO encapsula a lista de lançamentos paginada por cursor.
type LedgerPageDTO struct {
	Entries    []database.LedgerItemDTO `json:"entries"`
	NextCursor string                   `json:"nextCursor,omitempty"`
}

// GetLedger retorna o extrato com ordenação e cursor opaco.
func (s *WalletService) GetLedger(ctx context.Context, walletId, cursor string, limit int) (*LedgerPageDTO, error) {
	// Valida se a carteira existe
	if _, err := s.repo.GetWallet(ctx, walletId); err != nil {
		return nil, err
	}

	entries, nextCursor, err := s.repo.GetLedgerEntries(ctx, walletId, cursor, limit)
	if err != nil {
		return nil, err
	}

	return &LedgerPageDTO{
		Entries:    entries,
		NextCursor: nextCursor,
	}, nil
}
