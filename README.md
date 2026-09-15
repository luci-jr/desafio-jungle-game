# 🎲 Desafio Backend — Processamento Distribuído de Apostas em Go

Serviço financeiro concorrente e distribuído para processamento de apostas de provedores de jogos (*iGaming / Sportsbook*). Desenvolvido em **Go 1.26.4**, composto com **Uber Fx**, com persistência relacional ACID em **PostgreSQL 16**, mensageria assíncrona **AWS SQS FIFO** via **LocalStack**, e autenticação OIDC via **Keycloak**.

---

## 🚀 1. Pré-requisitos

- **Go:** versão `1.22+`
- **Docker & Docker Compose:** Docker 24+ com Compose v2+
- **Utilidades de linha de comando:** `curl`, `jq`, `make` (opcional)

---

## ⚙️ 2. Variáveis de Ambiente (`.env`)

A aplicação lê automaticamente o arquivo `.env` na raiz. Um arquivo de exemplo completo está versionado em [`.env.example`](.env.example):

```env
# Aplicação HTTP
APP_ENV=development
APP_PORT=8000
LOG_LEVEL=info

# Banco de Dados (PostgreSQL)
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=betting_db
DB_SSLMODE=disable
DB_MAX_CONNS=25
DB_MIN_CONNS=5

# AWS SQS FIFO / LocalStack
AWS_REGION=us-east-1
AWS_ENDPOINT=http://localhost:4566
AWS_ACCESS_KEY_ID=test
AWS_SECRET_ACCESS_KEY=test
SQS_QUEUE_URL=http://localhost:4566/000000000000/wager-transactions.fifo
SQS_DLQ_URL=http://localhost:4566/000000000000/wager-transactions-dlq.fifo
SQS_EVENT_QUEUE_URL=http://localhost:4566/000000000000/wager-events.fifo

# Autenticação OIDC (Keycloak)
AUTH_JWKS_URL=http://localhost:8080/realms/betting/protocol/openid-connect/certs
AUTH_ISSUER=http://localhost:8080/realms/betting
```

Para gerar seu arquivo local:
```bash
cp .env.example .env
```

---

## 🐳 3. Execução do Ambiente com Docker Compose

Para subir toda a infraestrutura (PostgreSQL, LocalStack com filas SQS FIFO provisionadas automaticamente e Keycloak com Realm e Clientes importados):

```bash
docker compose up -d
```

Caso queira rodar também a API Go conteinerizada:
```bash
docker compose up --build
```

### Execução Local (Go Nativo)
Caso prefira executar a aplicação Go diretamente no seu terminal (fora do Docker), mantendo os serviços de infraestrutura (PostgreSQL, LocalStack, Keycloak) em execução via Docker:
```bash
go run cmd/api/main.go
```

### Serviços Provisionados:
| Serviço | Container | Porta Host | Finalidade |
|---|---|---|---|
| **PostgreSQL 16** | `betting-postgres` | `5432` | Banco relacional ACID com constraints e triggers de imutabilidade |
| **LocalStack 3.7** | `betting-localstack` | `4566` | Emulador AWS (SQS FIFO + DLQ com Redrive Policy) |
| **Keycloak 24** | `betting-keycloak` | `8080` | IdP OAuth 2.0 / OIDC com realm `betting` importado |
| **API Go** | `betting-api` | `8000` | Servidor HTTP e Workers assíncronos Uber Fx |

---

## 🗄️ 4. Migrations do Banco de Dados

As migrations são aplicadas de forma automática pelo ciclo de vida (`fx.Lifecycle`) da aplicação Go ao iniciar.

Caso deseje executar ou reverter manualmente via terminal:

### Aplicação (Up):
```bash
docker exec -i betting-postgres psql -U postgres -d betting_db < migrations/000001_init_schema.up.sql
```

### Reversão (Down):
```bash
docker exec -i betting-postgres psql -U postgres -d betting_db < migrations/000001_init_schema.down.sql
```

---

## 🔑 5. Autenticação e Provedores de Teste (Keycloak)

O Keycloak inicializa com o realm `betting` e dois clientes pré-configurados com protocolo `client_credentials`:

| Cliente | Client ID | Client Secret | Claim `providerId` |
|---|---|---|---|
| Provedor A | `provider-a` | `secret-a` | `provider-a` |
| Provedor B | `provider-b` | `secret-b` | `provider-b` |
| Serviço interno | `internal-service` | `secret-internal` | `admin` |

### Como obter os tokens JWT de teste

Token do provedor, usado nas operações de aposta:
```bash
PROVIDER_TOKEN=$(curl -s -X POST http://localhost:8080/realms/betting/protocol/openid-connect/token \
  -d "grant_type=client_credentials" \
  -d "client_id=provider-a" \
  -d "client_secret=secret-a" | jq -r .access_token)

echo $PROVIDER_TOKEN
```

Token interno, usado nas operações de carteira e reconciliação:
```bash
INTERNAL_TOKEN=$(curl -s -X POST http://localhost:8080/realms/betting/protocol/openid-connect/token \
  -d "grant_type=client_credentials" \
  -d "client_id=internal-service" \
  -d "client_secret=secret-internal" | jq -r .access_token)
```

As rotas de carteira exigem `INTERNAL_TOKEN`. Os tokens dos provedores são
usados somente para enviar e consultar suas próprias apostas.

---

## 🧪 6. Execução de Testes Automatizados

O projeto conta com suíte completa de testes unitários, testes de concorrência com disputa de apostas, integridade de ledger e resiliência com containers reais.

### Preparação do Ambiente para os Testes
Para executar a suíte completa de integração e concorrência que depende de infraestrutura real, suba previamente os containers de apoio:
```bash
docker compose up -d postgres localstack keycloak
```

### Comandos de Teste Oficiais (Edital Seção 15):
```bash
# 1. Executar todos os testes da aplicação
go test ./...

# 2. Executar testes com detector de Race Conditions (-race)
go test -race ./...

# 3. Executar os testes de integração e concorrência com saída detalhada
go test -v -race ./tests/...

# 4. Verificação estática de código (Go Linter padrão)
go vet ./...
```

### Testes implementados

| Grupo | Cenários cobertos |
|---|---|
| Domínio | Parsing de `Money`, escala fixa, overflow, moedas incompatíveis, soma, subtração e serialização JSON |
| Carteira e ledger | Saldo não negativo, débito, crédito, versão, equação contábil e imutabilidade do ledger |
| Integração | Migrations, abertura de carteira, reconciliação e trigger append-only do ledger |
| Idempotência | Cinquenta requisições simultâneas da mesma aposta e replay persistente |
| Concorrência | Duas apostas de `80.00` sobre saldo de `100.00` |
| Autorização | Token ausente, isolamento entre provedores e bloqueio de carteira para provedores |
| Referência | `REFUND` antes da `BET`, estado `PENDING_REFERENCE` e resolução pelo worker |
| SQS | Consumidor FIFO, Inbox, deduplicação e alteração do saldo após consumo |

Os testes de integração usam PostgreSQL, LocalStack e Keycloak reais por Docker.
O Keycloak precisa ser recriado quando o `realm-export.json` mudar:

```bash
docker compose up -d postgres localstack
docker compose up -d --force-recreate keycloak
```

### Comandos executados na validação

```bash
# Compilar os testes sem executá-los
go test -run '^$' ./tests/...

# Rodar toda a suíte
go test ./...

# Rodar a suíte com logs detalhados
go test -v ./tests/...

# Verificar condições de corrida
go test -race ./...

# Verificar problemas estáticos
go vet ./...

# Construir a imagem sem usar cache
docker compose build --no-cache api

# Subir a API depois do build
docker compose up -d api

# Conferir os containers e logs
docker compose ps
docker compose logs --no-color --tail=120 api

# Conferir liveness e readiness
curl -i http://localhost:8000/health/live
curl -i http://localhost:8000/health/ready
```

O resultado esperado é `go test ./...` sem falhas e `go vet ./...` sem avisos.
Os testes de integração só devem ser executados depois que os containers de
PostgreSQL, LocalStack e Keycloak estiverem prontos.

---

## 📡 7. Mapa da API para Postman e chamadas HTTP

### 7.1. Configuração do ambiente no Postman

Crie um Environment com estas variáveis:

| Variável | Valor inicial | Uso |
|---|---|---|
| `baseUrl` | `http://localhost:8000` | URL da API |
| `keycloakUrl` | `http://localhost:8080` | URL do Keycloak |
| `providerId` | `provider-a` | Provedor dos testes |
| `providerToken` | vazio | Token JWT do provedor |
| `internalToken` | vazio | Token JWT interno |
| `walletId` | vazio | Preenchida após criar carteira |
| `betExternalId` | `postman-bet-001` | ID externo da aposta |

Para obter os tokens no Postman, crie uma requisição `POST` para:

```text
{{keycloakUrl}}/realms/betting/protocol/openid-connect/token
```

Use `Body > x-www-form-urlencoded`:

```text
grant_type=client_credentials
client_id=provider-a
client_secret=secret-a
```

Repita com `client_id=internal-service` e `client_secret=secret-internal`.
Copie o campo `access_token` para a variável correspondente.

### 7.2. Mapa dos endpoints

| Método | Endpoint | Token | Resultado esperado |
|---|---|---|---|
| `GET` | `/health/live` | Nenhum | `200` com processo ativo |
| `GET` | `/health/ready` | Nenhum | `200` com PostgreSQL e SQS conectados |
| `POST` | `/wallets` | `internalToken` | `201` e carteira criada |
| `GET` | `/wallets/{walletId}` | `internalToken` | `200` com saldo e versão |
| `GET` | `/wallets/{walletId}/ledger` | `internalToken` | `200` com lançamentos |
| `POST` | `/wallets/{walletId}/reconciliation` | `internalToken` | `200` com consistência contábil |
| `POST` | `/wagering/transactions` | `providerToken` | `200` com resultado da operação |
| `GET` | `/wagering/transactions/{transactionId}` | `providerToken` | `200` se pertencer ao provedor |
| `GET` | `/providers/{providerId}/wagering/transactions/{externalTransactionId}` | `providerToken` | `200` se o provedor for autorizado |

Todas as rotas de negócio precisam do header:

```text
Authorization: Bearer {{providerToken}}
```

Nas rotas de carteira, use `{{internalToken}}`. Em `POST /wagering/transactions`,
adicione também:

```text
Idempotency-Key: {{providerId}}:{{betExternalId}}
```

### 7.3. Ordem sugerida no Postman

1. Obtenha `providerToken` e `internalToken` no Keycloak.
2. Execute `POST /wallets` usando `internalToken` e salve o campo `id` em `walletId`.
3. Execute `GET /wallets/{{walletId}}` para conferir o saldo.
4. Execute `POST /wagering/transactions` usando `providerToken` e `Idempotency-Key`.
5. Repita a mesma aposta para observar `idempotentReplay: true`.
6. Consulte o ledger e execute a reconciliação usando `internalToken`.
7. Para `REFUND` ou `ROLLBACK`, use o `externalTransactionId` da operação original.

Os exemplos abaixo usam `curl` e reproduzem as mesmas requisições que podem ser
criadas no Postman.

### 7.4. Health Checks (Públicos)
```bash
# Liveness (saúde do processo)
curl -s http://localhost:8000/health/live

# Readiness (conectividade com Postgres e LocalStack SQS)
curl -s http://localhost:8000/health/ready
```

### 7.5. Criar Carteira com Saldo Inicial
```bash
curl -s -X POST http://localhost:8000/wallets \
  -H "Authorization: Bearer $INTERNAL_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "initialBalance": { "amount": "1000.00", "currency": "BRL" }
  }' | jq
```

### 7.6. Consultar Saldo da Carteira
```bash
curl -s http://localhost:8000/wallets/<WALLET_ID> \
  -H "Authorization: Bearer $INTERNAL_TOKEN" | jq
```

### 7.7. Consultar Extrato (Ledger) com Paginação por Cursor
```bash
curl -s "http://localhost:8000/wallets/<WALLET_ID>/ledger?limit=50" \
  -H "Authorization: Bearer $INTERNAL_TOKEN" | jq
```

### 7.8. Enviar Aposta (`BET`) com Chave de Idempotência
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-101" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-101",
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId": "<WALLET_ID>",
    "roundId": "round-987",
    "gameId": "fortune-chimp",
    "kind": "BET",
    "money": { "amount": "25.00", "currency": "BRL" }
  }' | jq
```

### 7.9. Enviar Prêmio (`WIN`)
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-102" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-102",
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId": "<WALLET_ID>",
    "roundId": "round-987",
    "gameId": "fortune-chimp",
    "kind": "WIN",
    "money": { "amount": "50.00", "currency": "BRL" }
  }' | jq
```

### 7.10. Enviar Estorno (`REFUND`) com Referência Externa
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-103" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-103",
    "referenceExternalTransactionId": "tx-101",
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId": "<WALLET_ID>",
    "roundId": "round-987",
    "gameId": "fortune-chimp",
    "kind": "REFUND",
    "money": { "amount": "25.00", "currency": "BRL" }
  }' | jq
```

### 7.11. Consultar Transação por Identificador Externo
```bash
curl -s http://localhost:8000/providers/provider-a/wagering/transactions/tx-101 \
  -H "Authorization: Bearer $PROVIDER_TOKEN" | jq
```

### 7.12. Reconciliação Contábil da Carteira
```bash
curl -s -X POST http://localhost:8000/wallets/<WALLET_ID>/reconciliation \
  -H "Authorization: Bearer $INTERNAL_TOKEN" | jq
```

---

## 📨 8. Envio de Mensagens SQS FIFO

```bash
docker exec -i betting-localstack awslocal sqs send-message \
  --queue-url http://localhost:4566/000000000000/wager-transactions.fifo \
  --message-group-id "wallet-<WALLET_ID>" \
  --message-deduplication-id "sqs-msg-101" \
  --message-body '{
    "messageId": "sqs-msg-101",
    "type": "WagerTransactionRequested",
    "occurredAt": "2026-09-14T22:00:00.000Z",
    "data": {
      "providerId": "provider-a",
      "externalTransactionId": "tx-sqs-101",
      "idempotencyKey": "provider-a:tx-sqs-101",
      "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
      "walletId": "<WALLET_ID>",
      "roundId": "round-987",
      "gameId": "fortune-chimp",
      "kind": "BET",
      "money": { "amount": "50.00", "currency": "BRL" }
    }
  }'
```
