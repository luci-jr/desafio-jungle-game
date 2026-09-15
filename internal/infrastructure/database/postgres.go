package database

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config define as configurações necessárias para conexão ao PostgreSQL.
type Config struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
	SSLMode  string
	MaxConns int32
	MinConns int32
}

// NewPostgresPool inicializa e valida o pool de conexões pgxpool.
func NewPostgresPool(cfg Config) (*pgxpool.Pool, error) {
	connStr := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database, cfg.SSLMode,
	)

	poolConfig, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("falha ao analisar string de conexão postgres: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = cfg.MaxConns
	} else {
		poolConfig.MaxConns = 25
	}

	if cfg.MinConns > 0 {
		poolConfig.MinConns = cfg.MinConns
	} else {
		poolConfig.MinConns = 5
	}

	poolConfig.MaxConnLifetime = 1 * time.Hour
	poolConfig.MaxConnIdleTime = 30 * time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("falha ao instanciar pgxpool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("falha ao conectar (ping) ao postgres: %w", err)
	}

	return pool, nil
}

// RunMigrations aplica o arquivo SQL de inicialização do schema no banco de dados.
func RunMigrations(pool *pgxpool.Pool, migrationPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sqlContent, err := os.ReadFile(migrationPath)
	if err != nil {
		return fmt.Errorf("falha ao ler arquivo de migração (%s): %w", migrationPath, err)
	}

	if _, err := pool.Exec(ctx, string(sqlContent)); err != nil {
		return fmt.Errorf("falha ao executar migração SQL: %w", err)
	}

	return nil
}
