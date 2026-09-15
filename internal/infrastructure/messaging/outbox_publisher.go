package messaging

import (
	"context"
	"log/slog"
	"time"

	"backend-challenge-go/internal/infrastructure/database"
)

// OutboxPublisherWorker varre e publica registros pendentes da tabela outbox_events.
type OutboxPublisherWorker struct {
	repo   *database.Repository
	logger *slog.Logger
}

func NewOutboxPublisherWorker(repo *database.Repository, logger *slog.Logger) *OutboxPublisherWorker {
	return &OutboxPublisherWorker{
		repo:   repo,
		logger: logger,
	}
}

// Start inicia o ciclo de publicação do outbox.
func (w *OutboxPublisherWorker) Start(ctx context.Context) {
	w.logger.Info("Iniciando OutboxPublisherWorker (Transactional Outbox)")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Encerrando OutboxPublisherWorker graciosamente...")
			return
		case <-ticker.C:
			w.publishBatch(ctx)
		}
	}
}

func (w *OutboxPublisherWorker) publishBatch(ctx context.Context) {
	// Busca registros com FOR UPDATE SKIP LOCKED para permitir múltiplos publicadores concorrentes
	records, err := w.repo.FetchPendingOutboxEvents(ctx, 20)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("Erro ao buscar eventos pendentes do outbox", "error", err)
		}
		return
	}

	for _, rec := range records {
		w.logger.Info(
			"Publicando evento da Outbox",
			"eventId", rec.EventID,
			"eventType", rec.EventType,
		)

		// Simulação / Entrega do evento no barramento
		// Em produção enviaria ao SNS/SQS/EventBridge; aqui marcamos como publicado
		if err := w.repo.MarkOutboxPublished(ctx, rec.ID); err != nil {
			w.logger.Error("Erro ao marcar evento do outbox como publicado", "eventId", rec.EventID, "error", err)
			nextRetry := time.Now().Add(5 * time.Second)
			_ = w.repo.IncrementOutboxRetry(ctx, rec.ID, nextRetry)
		}
	}
}
