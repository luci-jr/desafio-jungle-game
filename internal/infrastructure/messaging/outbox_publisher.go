package messaging

import (
	"context"
	"log/slog"
	"time"

	"backend-challenge-go/internal/infrastructure/database"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
)

// OutboxPublisherWorker varre e publica registros pendentes da tabela outbox_events.
type OutboxPublisherWorker struct {
	repo       *database.Repository
	sqsClient  *sqs.Client
	eventQueue string
	logger     *slog.Logger
	claimToken string
}

func NewOutboxPublisherWorker(
	repo *database.Repository,
	sqsClient *sqs.Client,
	cfg Config,
	logger *slog.Logger,
) *OutboxPublisherWorker {
	return &OutboxPublisherWorker{
		repo:       repo,
		sqsClient:  sqsClient,
		eventQueue: cfg.EventQueueURL,
		logger:     logger,
		claimToken: uuid.NewString(),
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
	// Reserva registros no banco para permitir múltiplos publishers sem disputa duplicada.
	records, err := w.repo.ClaimPendingOutboxEvents(ctx, 20, w.claimToken)
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

		_, err := w.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
			QueueUrl:               aws.String(w.eventQueue),
			MessageBody:            aws.String(string(rec.Payload)),
			MessageGroupId:         aws.String("aggregate-" + rec.AggregateID),
			MessageDeduplicationId: aws.String(rec.EventID),
		})
		if err != nil {
			w.logger.Error("Erro ao publicar evento no SQS", "eventId", rec.EventID, "error", err)
			nextRetry := time.Now().Add(outboxRetryDelay(rec.RetryCount))
			_ = w.repo.IncrementOutboxRetry(ctx, rec.ID, w.claimToken, nextRetry)
			continue
		}

		if err := w.repo.MarkOutboxPublished(ctx, rec.ID, w.claimToken); err != nil {
			w.logger.Error("Erro ao confirmar publicação do outbox", "eventId", rec.EventID, "error", err)
		}
	}
}

// outboxRetryDelay aplica backoff exponencial limitado para indisponibilidade do broker.
func outboxRetryDelay(retryCount int) time.Duration {
	if retryCount > 6 {
		retryCount = 6
	}
	return time.Duration(1<<retryCount) * time.Second
}
