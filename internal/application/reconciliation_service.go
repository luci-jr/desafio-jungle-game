package application

import (
	"context"
	"fmt"

	"backend-challenge-go/internal/domain"
	"backend-challenge-go/internal/infrastructure/database"
)

// ReconciliationService reconstrói a integridade matemática do saldo a partir do ledger.
type ReconciliationService struct {
	repo *database.Repository
}

func NewReconciliationService(repo *database.Repository) *ReconciliationService {
	return &ReconciliationService{repo: repo}
}

// ReconciliationResponseDTO é o contrato oficial de reconciliação exigido pela especificação.
type ReconciliationResponseDTO struct {
	WalletID          string       `json:"walletId"`
	StoredBalance     domain.Money `json:"storedBalance"`
	CalculatedBalance domain.Money `json:"calculatedBalance"`
	Difference        domain.Money `json:"difference"`
	Consistent        bool         `json:"consistent"`
	CheckedEntries    int64        `json:"checkedEntries"`
}

// ReconcileWallet compara o saldo persistido na carteira com o somatório de todos os lançamentos do ledger.
func (s *ReconciliationService) ReconcileWallet(ctx context.Context, walletId string) (*ReconciliationResponseDTO, error) {
	wallet, err := s.repo.GetWallet(ctx, walletId)
	if err != nil {
		return nil, err
	}

	calcCents, checkedEntries, err := s.repo.CalculateLedgerBalance(ctx, walletId)
	if err != nil {
		return nil, fmt.Errorf("erro ao calcular soma contábil do ledger: %w", err)
	}

	calculatedBalance, err := domain.NewMoney(calcCents, wallet.Currency())
	if err != nil {
		return nil, err
	}

	diff, err := wallet.Balance().Sub(calculatedBalance)
	if err != nil {
		return nil, err
	}

	consistent := diff.IsZero()

	return &ReconciliationResponseDTO{
		WalletID:          wallet.ID(),
		StoredBalance:     wallet.Balance(),
		CalculatedBalance: calculatedBalance,
		Difference:        diff,
		Consistent:        consistent,
		CheckedEntries:    checkedEntries,
	}, nil
}
