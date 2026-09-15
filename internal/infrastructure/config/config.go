package config

import (
	"os"
	"strconv"

	"backend-challenge-go/internal/infrastructure/database"
	"backend-challenge-go/internal/infrastructure/messaging"
)

type AppConfig struct {
	AppPort     string
	LogLevel    string
	DB          database.Config
	Messaging   messaging.Config
	AuthJWKSURL string
	AuthIssuer  string
}

func LoadConfig() AppConfig {
	maxConns, _ := strconv.Atoi(getEnv("DB_MAX_CONNS", "25"))
	minConns, _ := strconv.Atoi(getEnv("DB_MIN_CONNS", "5"))

	return AppConfig{
		AppPort:  getEnv("APP_PORT", "8000"),
		LogLevel: getEnv("LOG_LEVEL", "info"),
		DB: database.Config{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnv("DB_PORT", "5432"),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			Database: getEnv("DB_NAME", "betting_db"),
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
			MaxConns: int32(maxConns),
			MinConns: int32(minConns),
		},
		Messaging: messaging.Config{
			Region:    getEnv("AWS_REGION", "us-east-1"),
			Endpoint:  getEnv("AWS_ENDPOINT", "http://localhost:4566"),
			AccessKey: getEnv("AWS_ACCESS_KEY_ID", "test"),
			SecretKey: getEnv("AWS_SECRET_ACCESS_KEY", "test"),
			QueueURL:  getEnv("SQS_QUEUE_URL", "http://localhost:4566/000000000000/wager-transactions.fifo"),
			DLQURL:    getEnv("SQS_DLQ_URL", "http://localhost:4566/000000000000/wager-transactions-dlq.fifo"),
		},
		AuthJWKSURL: getEnv("AUTH_JWKS_URL", "http://localhost:8080/realms/betting/protocol/openid-connect/certs"),
		AuthIssuer:  getEnv("AUTH_ISSUER", "http://localhost:8080/realms/betting"),
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
