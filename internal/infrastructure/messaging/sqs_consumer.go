package messaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"backend-challenge-go/internal/application"
	"backend-challenge-go/internal/domain"
	"backend-challenge-go/internal/infrastructure/database"

	"errors"

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
			if isTerminalMessageError(err) {
				// Rejeições de negócio já auditadas não precisam de novas tentativas.
				if deleteErr := w.deleteMessage(ctx, msg); deleteErr != nil {
					w.logger.Error("Falha ao remover mensagem terminal do SQS", "messageId", *msg.MessageId, "error", deleteErr)
				}
			}
			// Falhas transitórias permanecem invisíveis até o timeout e seguem para a DLQ.
		} else {
			// Sucesso ou rejeição terminal definitiva: apaga mensagem da fila
			if err := w.deleteMessage(ctx, msg); err != nil {
				w.logger.Error("Falha ao remover mensagem processada do SQS", "messageId", *msg.MessageId, "error", err)
			}
		}
	}
}

// deleteMessage remove uma mensagem somente depois de um resultado durável.
func (w *SQSConsumerWorker) deleteMessage(ctx context.Context, msg types.Message) error {
	_, err := w.sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(w.queueURL),
		ReceiptHandle: msg.ReceiptHandle,
	})
	return err
}

// isTerminalMessageError distingue rejeições de negócio de indisponibilidade.
func isTerminalMessageError(err error) bool {
	return errors.Is(err, domain.ErrIdempotencyConflict) ||
		errors.Is(err, domain.ErrCurrencyMismatch) ||
		errors.Is(err, domain.ErrInvalidTransactionKind) ||
		errors.Is(err, domain.ErrReferenceNotFound) ||
		errors.Is(err, domain.ErrReferenceNotProcessed) ||
		errors.Is(err, domain.ErrReferenceMismatch) ||
		errors.Is(err, domain.ErrReferenceAlreadyReversed) ||
		errors.Is(err, domain.ErrOpeningNotAllowedExternally) ||
		errors.Is(err, domain.ErrInvalidExternalPayload) ||
		errors.Is(err, domain.ErrInboxPayloadConflict)
}

func (w *SQSConsumerWorker) handleMessage(ctx context.Context, msg types.Message) error {
	if msg.Body == nil {
		return fmt.Errorf("mensagem SQS sem corpo")
	}

	payloadHashBytes := sha256.Sum256([]byte(*msg.Body))
	payloadHash := hex.EncodeToString(payloadHashBytes[:])

	var env SQSMessageEnvelope
	if err := json.Unmarshal([]byte(*msg.Body), &env); err != nil {
		w.logger.Error("Mensagem inválida no SQS; mantendo para retry/DLQ", "error", err)
		return fmt.Errorf("mensagem SQS inválida: %w", err)
	}
	if env.MessageID == "" || env.Type != "WagerTransactionRequested" {
		return fmt.Errorf("envelope SQS inválido: messageId e type são obrigatórios")
	}

	consumerName := "wager-transactions-sqs-consumer"

	var data SQSDataPayload
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return fmt.Errorf("payload SQS inválido: %w", err)
	}

	// A Inbox e a operação financeira compartilham esta transação SQL.
	tx, err := w.repo.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	inserted, err := w.repo.TrySaveInboxMessage(ctx, tx, consumerName, env.MessageID, payloadHash)
	if err != nil {
		return err
	}
	if !inserted {
		w.logger.Info("Mensagem já processada anteriormente", "messageId", env.MessageID)
		return tx.Commit(ctx)
	}

	// Chama o caso de uso unificado dentro da mesma transação da Inbox.
	_, err = w.wagerService.ProcessTransactionInTx(ctx, tx, application.WagerRequestDTO{
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
		return err
	}

	return tx.Commit(ctx)
}
