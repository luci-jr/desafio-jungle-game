package messaging

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"backend-challenge-go/internal/application"
	"backend-challenge-go/internal/domain"
	"backend-challenge-go/internal/infrastructure/database"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// SQSMessageEnvelope encapsula a mensagem recebida pelo SQS FIFO.
type SQSMessageEnvelope struct {
	MessageID  string          `json:"messageId"`
	Type       string          `json:"type"`
	OccurredAt string          `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

// SQSDataPayload representa os dados de negócio da mensagem SQS.
type SQSDataPayload struct {
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

// SQSConsumerWorker consome e processa mensagens da fila SQS FIFO com garantia de Inbox e idempotência.
type SQSConsumerWorker struct {
	sqsClient    *sqs.Client
	queueURL     string
	wagerService *application.WagerService
	repo         *database.Repository
	logger       *slog.Logger
}

func NewSQSConsumerWorker(
	sqsClient *sqs.Client,
	cfg Config,
	wagerService *application.WagerService,
	repo *database.Repository,
	logger *slog.Logger,
) *SQSConsumerWorker {
	return &SQSConsumerWorker{
		sqsClient:    sqsClient,
		queueURL:     cfg.QueueURL,
		wagerService: wagerService,
		repo:         repo,
		logger:       logger,
	}
}

// Start inicia o loop de consumo contínuo com suporte a encerramento gracioso via context.
func (w *SQSConsumerWorker) Start(ctx context.Context) {
	w.logger.Info("Iniciando SQSConsumerWorker para fila FIFO", "queue", w.queueURL)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Encerrando SQSConsumerWorker graciosamente...")
			return
		default:
			w.pollAndProcess(ctx)
		}
	}
}

func (w *SQSConsumerWorker) pollAndProcess(ctx context.Context) {
	out, err := w.sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(w.queueURL),
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     5, // Long polling
		VisibilityTimeout:   30,
	})
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.logger.Error("Erro ao receber mensagens do SQS", "error", err)
		time.Sleep(2 * time.Second)
		return
	}

	for _, msg := range out.Messages {
		if err := w.handleMessage(ctx, msg); err != nil {
			w.logger.Error("Falha ao processar mensagem do SQS", "messageId", *msg.MessageId, "error", err)
			// Falha transitória: a mensagem voltará após o VisibilityTimeout ou irá para a DLQ após 3 tentativas
		} else {
			// Sucesso ou rejeição terminal definitiva: apaga mensagem da fila
			_, _ = w.sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(w.queueURL),
				ReceiptHandle: msg.ReceiptHandle,
			})
		}
	}
}

func (w *SQSConsumerWorker) handleMessage(ctx context.Context, msg types.Message) error {
	var env SQSMessageEnvelope
	if err := json.Unmarshal([]byte(*msg.Body), &env); err != nil {
		w.logger.Error("Mensagem inválida no SQS (malformed json), descartando para DLQ", "body", *msg.Body)
		return nil // Permite descarte para não travar a fila com poison pills
	}

	consumerName := "wager-transactions-sqs-consumer"

	// 1. Inbox: Verifica se a mensagem já foi processada anteriormente
	hasProcessed, err := w.repo.HasInboxMessage(ctx, consumerName, env.MessageID)
	if err != nil {
		return err
	}
	if hasProcessed {
		w.logger.Info("Mensagem já processada anteriormente (Inbox deduplication)", "messageId", env.MessageID)
		return nil
	}

	var data SQSDataPayload
	if err := json.Unmarshal(env.Data, &data); err != nil {
		w.logger.Error("Payload interno de dados inválido", "error", err)
		return nil
	}

	// 2. Chama o caso de uso unificado (compartilhado com a API HTTP)
	_, err = w.wagerService.ProcessTransaction(ctx, application.WagerRequestDTO{
		ProviderID:                     data.ProviderID,
		ExternalTransactionID:          data.ExternalTransactionID,
		IdempotencyKey:                 data.IdempotencyKey,
		PlayerID:                       data.PlayerID,
		WalletID:                       data.WalletID,
		RoundID:                        data.RoundID,
		GameID:                         data.GameID,
		Kind:                           data.Kind,
		Money:                          data.Money,
		ReferenceExternalTransactionID: data.ReferenceExternalTransactionID,
	})
	if err != nil {
		// Se for conflito de idempotência ou erro de negócio, é terminal
		w.logger.Warn("Transação SQS rejeitada ou com conflito", "error", err)
		return nil
	}

	// 3. Registra na inbox que esta mensagem foi concluída com sucesso
	tx, err := w.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := w.repo.SaveInboxMessage(ctx, tx, consumerName, env.MessageID, "processed"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
