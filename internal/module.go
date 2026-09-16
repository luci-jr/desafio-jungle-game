package internal

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"backend-challenge-go/internal/application"
	"backend-challenge-go/internal/infrastructure/auth"
	"backend-challenge-go/internal/infrastructure/config"
	"backend-challenge-go/internal/infrastructure/database"
	internalHttp "backend-challenge-go/internal/infrastructure/http"
	"backend-challenge-go/internal/infrastructure/messaging"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

// Module consolida todos os componentes com injeção de dependências do Uber Fx.
var Module = fx.Options(
	// Provedores de Infraestrutura e Configuração
	fx.Provide(
		func() *slog.Logger {
			return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
		},
		config.LoadConfig,
		func(cfg config.AppConfig) database.Config {
			return cfg.DB
		},
		func(cfg config.AppConfig) messaging.Config {
			return cfg.Messaging
		},
		database.NewPostgresPool,
		database.NewRepository,
		func(cfg config.AppConfig) *auth.TokenValidator {
			return auth.NewTokenValidator(cfg.AuthJWKSURL, cfg.AuthIssuer)
		},
		messaging.NewSQSClient,
	),

	// Provedores de Casos de Uso (Application Services)
	fx.Provide(
		application.NewWalletService,
		application.NewWagerService,
		application.NewReconciliationService,
	),

	// Provedores de Workers Assíncronos
	fx.Provide(
		messaging.NewSQSConsumerWorker,
		messaging.NewOutboxPublisherWorker,
		messaging.NewPendingReferenceWorker,
	),

	// Provedores de HTTP
	fx.Provide(
		internalHttp.NewHandler,
		internalHttp.NewRouter,
	),

	// Invocação do Ciclo de Vida da Aplicação
	fx.Invoke(registerLifecycle),
)

func registerLifecycle(
	lc fx.Lifecycle,
	cfg config.AppConfig,
	pool *pgxpool.Pool,
	router http.Handler,
	sqsWorker *messaging.SQSConsumerWorker,
	outboxWorker *messaging.OutboxPublisherWorker,
	pendingRefWorker *messaging.PendingReferenceWorker,
	logger *slog.Logger,
) {
	server := &http.Server{
		Addr:    ":" + cfg.AppPort,
		Handler: router,
	}

	workerCtx, cancelWorkers := context.WithCancel(context.Background())

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			logger.Info("Iniciando Desafio Jungle Game - Go Backend")

			// 1. Aplicação das Migrations no PostgreSQL
			migrationFile := "migrations/000001_init_schema.up.sql"
			if _, err := os.Stat(migrationFile); err == nil {
				logger.Info("Aplicando migrations de schema no PostgreSQL...", "file", migrationFile)
				if err := database.RunMigrations(pool, migrationFile); err != nil {
					logger.Error("Erro ao aplicar migrations no PostgreSQL", "error", err)
					return err
				}
				logger.Info("Migrations aplicadas com sucesso!")
			}

			// 2. Inicialização dos Workers em Goroutines
			go sqsWorker.Start(workerCtx)
			go outboxWorker.Start(workerCtx)
			go pendingRefWorker.Start(workerCtx)

			// 3. Inicialização do Servidor HTTP
			go func() {
				logger.Info("Servidor HTTP ouvindo na porta", "port", cfg.AppPort)
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Error("Erro fatal no servidor HTTP", "error", err)
				}
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("Encerrando aplicação graciosamente (Graceful Shutdown)...")

			// 1. Sinaliza cancelamento aos workers
			cancelWorkers()

			// 2. Encerra servidor HTTP com timeout
			shutdownCtx, cancelHTTP := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelHTTP()
			_ = server.Shutdown(shutdownCtx)

			// 3. Fecha o pool do banco de dados
			pool.Close()
			logger.Info("Recursos liberados com sucesso!")
			return nil
		},
	})
}
