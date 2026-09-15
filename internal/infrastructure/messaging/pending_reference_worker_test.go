package messaging

import (
	"errors"
	"testing"
	"time"

	"backend-challenge-go/internal/domain"
)

func TestValidateReferenceFieldsRejectsDifferentAmount(t *testing.T) {
	now := time.Now().UTC()
	refundMoney, err := domain.NewMoneyFromDecimal("30.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	betMoney, err := domain.NewMoneyFromDecimal("25.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	pending, err := domain.NewExternalTransaction(
		"refund-id", "provider-a", "refund-1", "provider-a:refund-1",
		"wallet-1", "player-1", "round-1", "game-1", domain.KindRefund,
		refundMoney, "bet-1", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := domain.NewExternalTransaction(
		"bet-id", "provider-a", "bet-1", "provider-a:bet-1",
		"wallet-1", "player-1", "round-1", "game-1", domain.KindBet,
		betMoney, "", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ref.TransitionToProcessed(betMoney, now); err != nil {
		t.Fatal(err)
	}

	err = validateReferenceFields(pending, ref)
	if !errors.Is(err, domain.ErrReferenceMismatch) {
		t.Fatalf("expected ErrReferenceMismatch, got %v", err)
	}
}
