# 🧪 Guia Mestre de Testes, Cenários e Fundamentos Tecnológicos
### Desafio Jungle Game — Backend Challenge Go

Este documento é o manual prático e didático definitivo para execução de testes, auditoria de conformidade e compreensão profunda de cada tecnologia corporativa empregada no **Desafio Jungle Game**.

Aqui, cada teste não é apenas um comando de terminal: é uma aula prática sobre como **sistemas distribuídos de alta concorrência**, **sistemas financeiros com tolerância a falhas** e **arquiteturas orientadas a eventos** funcionam por baixo dos panos.

---

## 📌 Sumário
1. [Visão Geral da Arquitetura & Stack Tecnológica](#1-visão-geral-da-arquitetura--stack-tecnológica)
2. [Execução Rápida da Suíte Automatizada](#2-execução-rápida-da-suíte-automatizada)
3. [Etapa 1: Setup de Autenticação OIDC & Multi-Tenancy (Keycloak 24)](#3-etapa-1-setup-de-autenticação-oidc--multi-tenancy-keycloak-24)
4. [Etapa 2: Fluxo Principal de Sucesso (Happy Path)](#4-etapa-2-fluxo-principal-de-sucesso-happy-path)
   - [2.1 Health Check da Aplicação](#21-health-check-da-aplicação)
   - [2.2 Criação Atômica de Carteira](#22-criação-atômica-de-carteira)
   - [2.3 Consulta de Saldo e Livro-Razão (Ledger)](#23-consulta-de-saldo-e-livro-razão-ledger)
   - [2.4 Transação de Débito: Aposta (BET)](#24-transação-de-débito-aposta-bet)
   - [2.5 Idempotência: Replay de Transação Idêntica](#25-idempotência-replay-de-transação-idêntica)
   - [2.6 Transação de Crédito: Ganho/Prêmio (WIN)](#26-transação-de-crédito-ganhoprêmio-win)
   - [2.7 Transação de Estorno: Cancelamento (REFUND)](#27-transação-de-estorno-cancelamento-refund)
   - [2.8 Reconciliação Matemática Zero-Divergence](#28-reconciliação-matemática-zero-divergence)
   - [2.9 Mensageria Assíncrona SQS FIFO (LocalStack)](#29-mensageria-assíncrona-sqs-fifo-localstack)
5. [Etapa 3: Matriz de Resiliência e Casos de Borda (Unhappy Path)](#5-etapa-3-matriz-de-resiliência-e-casos-de-borda-unhappy-path)
   - [Cenário E1: Carteira Duplicada (409 Conflict)](#cenário-e1-carteira-duplicada-409-conflict)
   - [Cenário E2: Saldo Insuficiente (Rejeição Atômica Auditável)](#cenário-e2-saldo-insuficiente-rejeição-atômica-auditável)
   - [Cenário E3: Moeda Divergente (422 Unprocessable Entity)](#cenário-e3-moeda-divergente-422-unprocessable-entity)
   - [Cenário E4: Conflito de Idempotência / Payload Adulterado (409 Conflict)](#cenário-e4-conflito-de-idempotência--payload-adulterado-409-conflict)
   - [Cenário E5: Header Mandatório Ausente (400 Bad Request)](#cenário-e5-header-mandatório-ausente-400-bad-request)
   - [Cenário E6: Falha de Autenticação / Token Inválido (401 Unauthorized)](#cenário-e6-falha-de-autenticação--token-inválido-401-unauthorized)
   - [Cenário E7: Violação de Tenancy / Provedor Invasor (403 Forbidden)](#cenário-e7-violação-de-tenancy--provedor-invasor-403-forbidden)
   - [Cenário E8-A: Estorno Fora de Ordem (Chegada Precoce -> PENDING_REFERENCE)](#cenário-e8-a-estorno-fora-de-ordem-chegada-precoce---pending_reference)
   - [Cenário E8-B: Estorno Duplicado de Aposta Já Revertida (422 Unprocessable)](#cenário-e8-b-estorno-duplicado-de-aposta-já-revertida-422-unprocessable)
   - [Cenário E9: Violação da Imutabilidade do Ledger via SQL (Trigger de Banco)](#cenário-e9-violação-da-imutabilidade-do-ledger-via-sql-trigger-de-banco)
   - [Cenário E10: Valor Zero ou Negativo Rejeitado (422 Unprocessable)](#cenário-e10-valor-zero-ou-negativo-rejeitado-422-unprocessable)
6. [Tabela Resumo de Códigos HTTP e Tratamento Arquitetural](#6-tabela-resumo-de-códigos-http-e-tratamento-arquitetural)

---

## 1. Visão Geral da Arquitetura & Stack Tecnológica

O sistema foi concebido para suportar alto rendimento transacional de apostas esportivas e jogos de cassino (*iGaming*), onde consistência monetária, rastreabilidade fiscal e isolamento entre provedores são inegociáveis.

### Diagrama Arquitetural em ASCII

```text
       [ Provedores Externos ]            [ Operações Internas ]
       (provider-a, provider-b)             (internal-service)
                  │                                  │
                  ▼                                  ▼
      ┌─────────────────────────────────────────────────────────┐
      │         Keycloak 24.0 (OIDC / OAuth2 Provider)         │
      │   - Emite JWT RS256 via Client Credentials Grant        │
      │   - Segrega tenants com claims azp e providerId         │
      └───────────────────────────┬─────────────────────────────┘
                                  │ Token Bearer
                                  ▼
      ┌─────────────────────────────────────────────────────────┐
      │               API Go 1.22+ (Chi Router + Fx)            │
      │                                                         │
      │  [Middleware Auth] ──> Validação JWKS / Cache de Chaves │
      │  [Middleware Tenant] ──> Bloqueio de Acesso Cruzado     │
      │  [WagerService] ──> Máquina de Estados & Hash SHA-256   │
      │  [Money VO] ──> Centavos Inteiros (int64, zero float)   │
      └──────────────┬───────────────────────────┬──────────────┘
                     │                           │
         Escrita     │               Consumo     │  Publicação
         Atômica     │               Mensagens   │  Outbox
                     ▼                           ▼
      ┌─────────────────────────────┐  ┌─────────────────────────────┐
      │     PostgreSQL 16 (ACID)    │  │  LocalStack 3.7 (AWS SQS)   │
      │                             │  │                             │
      │ • wallets (FOR UPDATE Lock) │  │ • wager-transactions.fifo   │
      │ • wallet_ledger_entries     │  │   - MessageGroupId          │
      │   (Trigger Imutabilidade)   │  │   - MessageDeduplicationId  │
      │ • wager_transactions        │  │ • wager-events.fifo (Outbox)│
      │ • outbox_events             │  └──────────────┬──────────────┘
      │ • inbox_messages            │                 │
      └─────────────────────────────┘                 ▼
                     ▲                ┌──────────────────────────────┐
                     │                │      Background Workers      │
                     └────────────────┤ • SQSConsumerWorker          │
                                      │ • OutboxPublisherWorker      │
                                      │ • PendingReferenceWorker     │
                                      └──────────────────────────────┘
```

---

## 2. Execução Rápida da Suíte Automatizada

Para validar todos os 21 cenários de teste de ponta a ponta com um único comando no terminal:

```bash
# Executa a suíte E2E automatizada completa
./scripts/test_e2e.sh
```

Ou para rodar a suíte interna de testes unitários e de integração com o detector de concorrência ativo (*Race Detector*):

```bash
go test -v -count=1 -race ./...
```

---

## 3. Etapa 1: Setup de Autenticação OIDC & Multi-Tenancy (Keycloak 24)

### 📚 Fundamento Tecnológico
- **Tecnologia:** Keycloak 24.0 executando sobre OpenJDK em modo containerizado.
- **Protocolo:** OpenID Connect (OIDC) / OAuth 2.0 via fluxo `client_credentials`.
- **Como Funciona por Baixo dos Panos:**
  1. A aplicação cliente envia `client_id` e `client_secret` diretamente para o Keycloak via POST.
  2. O Keycloak valida as credenciais contra a base de dados interna do realm `betting` e assina um token JWT utilizando sua **chave privada RSA** (algoritmo `RS256`).
  3. No cabeçalho do token vai o identificador da chave pública (`kid`).
  4. A API Go obtém periodicamente o conjunto de chaves públicas do Keycloak através do endpoint JWKS (`/certs`) e valida a assinatura criptográfica em microssegundos localmente na memória RAM, **sem precisar consultar o Keycloak a cada requisição**.
- **Por que isso é crítico:** Garante autenticação sem estado (*stateless*), altíssima performance e isolamento criptográfico entre diferentes empresas ou operadoras (*multi-tenancy*).

### Script de Setup dos Tokens
Copie e cole no teu terminal para exportar as variáveis de sessão:

```bash
# 1. Token para Operações Internas (Admin/Sistema - Criação de carteira e reconciliação)
export TOKEN_INTERNAL=$(curl -s -H "Host: keycloak:8080" -X POST http://localhost:8080/realms/betting/protocol/openid-connect/token \
  -d "grant_type=client_credentials" \
  -d "client_id=internal-service" \
  -d "client_secret=secret-internal" | jq -r .access_token)

# 2. Token do Provedor de Jogos A (provider-a)
export TOKEN_PROV_A=$(curl -s -H "Host: keycloak:8080" -X POST http://localhost:8080/realms/betting/protocol/openid-connect/token \
  -d "grant_type=client_credentials" \
  -d "client_id=provider-a" \
  -d "client_secret=secret-a" | jq -r .access_token)

# 3. Token do Provedor de Jogos B (provider-b) - Para testes de isolamento de tenant
export TOKEN_PROV_B=$(curl -s -H "Host: keycloak:8080" -X POST http://localhost:8080/realms/betting/protocol/openid-connect/token \
  -d "grant_type=client_credentials" \
  -d "client_id=provider-b" \
  -d "client_secret=secret-b" | jq -r .access_token)

# Validação rápida de integridade dos tokens gerados
echo "Token Interno:    ${TOKEN_INTERNAL:0:25}..."
echo "Token Provedor A: ${TOKEN_PROV_A:0:25}..."
echo "Token Provedor B: ${TOKEN_PROV_B:0:25}..."
```

---

## 4. Etapa 2: Fluxo Principal de Sucesso (Happy Path)

---

### 2.1 Health Check da Aplicação

#### 📚 Fundamento Tecnológico
- **Tecnologia:** HTTP Probe + Driver `pgxpool` (PostgreSQL) + AWS SDK Go v2 (`sqs.GetQueueAttributes`).
- **Como Funciona por Baixo dos Panos:**
  O endpoint `/health/ready` executa um ping real no pool de conexões do PostgreSQL (`pgxpool.Ping`) e consulta os atributos da fila SQS FIFO no LocalStack. Se qualquer um dos serviços falhar ou sofrer timeout, a aplicação responde `503 Service Unavailable`.
- **Por que isso é crítico:** Orquestradores como Kubernetes e Docker Swarm utilizam esse endpoint (*Readiness Probe*) para saber se devem encaminhar tráfego de usuários para este container.

#### Comando no Terminal
```bash
curl -s http://localhost:8000/health/ready | jq .
```

#### Resposta Esperada (`200 OK`)
```json
{
  "database": "CONNECTED",
  "sqs": "CONNECTED",
  "status": "UP"
}
```

---

### 2.2 Criação Atômica de Carteira

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Go Structs imutáveis + Transação SQL ACID (`BEGIN ... COMMIT`) + Value Object `Money`.
- **Como Funciona por Baixo dos Panos:**
  1. O valor de R$ 500,00 é parseado para o tipo `domain.Money`. Internamente, ele é armazenado como **`50000` centavos em um inteiro de 64 bits (`int64`)**. Ponto flutuante IEEE-754 é terminantemente proibido para evitar imprecisão matemática (como `0.1 + 0.2 = 0.30000000000000004`).
  2. Em uma **única transação SQL**, a API insere a carteira na tabela `wallets` e grava o primeiro registro contábil de crédito na tabela `wallet_ledger_entries` (Origem: `OPENING`).
  3. Se houver falha de rede ou queda de energia no milissegundo intermediário, o PostgreSQL faz `ROLLBACK` automático, garantindo que não existam carteiras sem livro-razão.

#### Comando no Terminal
```bash
export PLAYER_ID="player-teste-$(date +%s)"

WALLET_RESP=$(curl -s -X POST http://localhost:8000/wallets \
  -H "Authorization: Bearer $TOKEN_INTERNAL" \
  -H "Content-Type: application/json" \
  -d '{
    "playerId": "'"$PLAYER_ID"'",
    "initialBalance": {
      "amount": "500.00",
      "currency": "BRL"
    }
  }')

echo "$WALLET_RESP" | jq .
export WALLET_ID=$(echo "$WALLET_RESP" | jq -r .id)
echo "Carteira criada com ID: $WALLET_ID"
```

#### Resposta Esperada (`201 Created`)
```json
{
  "id": "e4a2c1f8-9b34-4a22-92df-87f54c412e0a",
  "playerId": "player-teste-1789523000",
  "balance": {
    "amount": "500.00",
    "currency": "BRL"
  },
  "version": 1
}
```
*Dissecação:* `version: 1` indica o contador de concorrência otimista da carteira na sua abertura.

---

### 2.3 Consulta de Saldo e Livro-Razão (Ledger)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Paginação por Cursor (*Cursor-Based Pagination*) + Contabilidade de Partidas Dobradas.
- **Como Funciona por Baixo dos Panos:**
  Em sistemas bancários de alto volume, paginação tradicional por offset (`OFFSET 10000`) é ineficiente porque o banco precisa ler e descartar 10 mil linhas. Aqui, utiliza-se paginação por cursor baseada no `created_at` e `id` do último registro (`WHERE created_at < $cursor LIMIT $limit`), mantendo o custo da query em tempo constante $O(1)$ indexado por B-Tree.
- **Por que isso é crítico:** Auditoria financeira instantânea de cada centavo que entrou ou saiu da carteira do jogador.

#### Comando no Terminal
```bash
# Consulta de Saldo Atual
curl -s -X GET http://localhost:8000/wallets/$WALLET_ID \
  -H "Authorization: Bearer $TOKEN_INTERNAL" | jq .

# Consulta de Lançamentos do Ledger (Livro-Razão)
curl -s -X GET "http://localhost:8000/wallets/$WALLET_ID/ledger?limit=10" \
  -H "Authorization: Bearer $TOKEN_INTERNAL" | jq .
```

#### Resposta Esperada do Ledger (`200 OK`)
```json
{
  "entries": [
    {
      "id": "f81b672b-8a56-4c3e-b819-25f0376174a9",
      "walletId": "e4a2c1f8-9b34-4a22-92df-87f54c412e0a",
      "transactionId": "7d643194-e0eb-4859-9945-8f67e1a38210",
      "direction": "CREDIT",
      "amount": { "amount": "500.00", "currency": "BRL" },
      "balanceBefore": { "amount": "0.00", "currency": "BRL" },
      "balanceAfter": { "amount": "500.00", "currency": "BRL" },
      "createdAt": "2026-09-15T22:00:00.123456Z"
    }
  ],
  "nextCursor": null
}
```

---

### 2.4 Transação de Débito: Aposta (BET)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Bloqueio Pessimista de Linha (`SELECT ... FOR UPDATE`) + SHA-256 Canonical Hash.
- **Como Funciona por Baixo dos Panos:**
  1. Quando a requisição de débito de R$ 50,00 chega, o Go abre uma transação SQL e executa:
     ```sql
     SELECT id, balance, version FROM wallets WHERE id = $1 FOR UPDATE;
     ```
  2. O PostgreSQL coloca um **lock exclusivo a nível de linha** na carteira. Se outra goroutine tentar debitar a mesma carteira simultaneamente, ela é suspensa na fila do banco até que esta transação faça `COMMIT`.
  3. Carteiras de **outros jogadores** não são afetadas e continuam sendo processadas 100% em paralelo.
  4. O saldo é subtraído com segurança (`500.00 - 50.00 = 450.00`), o lançamento de débito é gravado no ledger, a versão vai para `2` e o evento é inserido na tabela `outbox_events`.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-bet-001" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-bet-001",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "BET",
    "money": {
      "amount": "50.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`200 OK`)
```json
{
  "transactionId": "b139ec4a-58de-4889-b1d5-bc440a3311f2",
  "status": "PROCESSED",
  "balance": {
    "amount": "450.00",
    "currency": "BRL"
  },
  "idempotentReplay": false
}
```

---

### 2.5 Idempotência: Replay de Transação Idêntica

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Hash Criptográfico SHA-256 de Negócio + Constraint `UNIQUE(provider_id, idempotency_key)`.
- **Como Funciona por Baixo dos Panos:**
  1. A operadora do jogo reenvia **exatamente a mesma requisição** com a chave `provider-a:tx-bet-001` (cenário típico de timeout de rede onde o provedor não soube se o débito ocorreu).
  2. A API Go calcula o hash SHA-256 canônico do payload recebido e consulta a tabela `wager_transactions`.
  3. Como a chave já existe e os hashes são idênticos, a aplicação **não altera o saldo**, não gera débito repetido e responde imediatamente com o snapshot histórico do processamento original.
- **Por que isso é crítico:** Elimina o risco de "duplo clique" ou retries automáticos de rede que causariam cobrança indevida ao usuário.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-bet-001" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-bet-001",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "BET",
    "money": {
      "amount": "50.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`200 OK`)
```json
{
  "transactionId": "b139ec4a-58de-4889-b1d5-bc440a3311f2",
  "status": "PROCESSED",
  "balance": {
    "amount": "450.00",
    "currency": "BRL"
  },
  "idempotentReplay": true
}
```
*Dissecação:* `"idempotentReplay": true` sinaliza ao provedor que a operação já havia sido concluída com sucesso e nenhum centavo adicional foi debitado.

---

### 2.6 Transação de Crédito: Ganho/Prêmio (WIN)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Máquina de Estados Finita (`Kind: WIN`) + Incremento Atômico de Saldo.
- **Como Funciona por Baixo dos Panos:**
  O provedor reporta que o jogador ganhou R$ 120,00 na rodada `round-slots-01`. O agregado da carteira valida que o valor é estritamente positivo, executa o lock da carteira e soma o valor: `450.00 + 120.00 = 570.00`. Um lançamento de crédito é apensado ao ledger.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-win-001" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-win-001",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "WIN",
    "money": {
      "amount": "120.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`200 OK`)
```json
{
  "transactionId": "d9852a11-7391-447b-99a3-5c8e42f78901",
  "status": "PROCESSED",
  "balance": {
    "amount": "570.00",
    "currency": "BRL"
  },
  "idempotentReplay": false
}
```

---

### 2.7 Transação de Estorno: Cancelamento (REFUND)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Validação Referencial Cruzada + Proteção Anti-Double Refund.
- **Como Funciona por Baixo dos Panos:**
  1. A requisição de estorno carrega `referenceExternalTransactionId: "tx-bet-001"`.
  2. O serviço localiza a transação referenciada no banco de dados e verifica se pertence ao mesmo provedor, mesmo jogador, mesma moeda e mesmo valor.
  3. O método `HasSuccessfulReversal` verifica se a aposta original já não foi estornada anteriormente.
  4. Estando tudo válido, o débito de R$ 50,00 é devolvido como crédito: `570.00 + 50.00 = 620.00`.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-refund-001" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-refund-001",
    "referenceExternalTransactionId": "tx-bet-001",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "REFUND",
    "money": {
      "amount": "50.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`200 OK`)
```json
{
  "transactionId": "c33921f0-51c3-4d70-a3e9-01476592dd81",
  "status": "PROCESSED",
  "balance": {
    "amount": "620.00",
    "currency": "BRL"
  },
  "idempotentReplay": false
}
```

---

### 2.8 Reconciliação Matemática Zero-Divergence

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Algoritmo de Reconciliação Contábil + Agregação Pura de Histórico.
- **Como Funciona por Baixo dos Panos:**
  O endpoint `/reconciliation` reconstrói o saldo da carteira do zero através da soma algébrica de todas as entradas registradas no livro-razão:
  $$\text{Saldo Reconstruído} = \sum \text{Créditos} - \sum \text{Débitos}$$
  Em seguida, compara com o saldo armazenado na tabela `wallets`. A diferença calculada deve ser rigorosamente zero:
  $$\text{difference} = \text{storedBalance} - \text{calculatedBalance} = 0.00$$
- **Por que isso é crítico:** Prova matematicamente para órgãos reguladores de auditoria (como a Receita Federal e comissões de apostas) que não houve corrupção de memória nem adulteração manual de dados.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wallets/$WALLET_ID/reconciliation \
  -H "Authorization: Bearer $TOKEN_INTERNAL" | jq .
```

#### Resposta Esperada (`200 OK`)
```json
{
  "walletId": "e4a2c1f8-9b34-4a22-92df-87f54c412e0a",
  "consistent": true,
  "checkedEntries": 4,
  "storedBalance": {
    "amount": "620.00",
    "currency": "BRL"
  },
  "calculatedBalance": {
    "amount": "620.00",
    "currency": "BRL"
  },
  "difference": {
    "amount": "0.00",
    "currency": "BRL"
  }
}
```

---

### 2.9 Mensageria Assíncrona SQS FIFO (LocalStack)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** AWS SQS FIFO (First-In, First-Out) + `MessageGroupId` + `MessageDeduplicationId` + Background Worker em Go.
- **Como Funciona por Baixo dos Panos:**
  1. O evento é postado na fila SQS FIFO com `MessageGroupId: "wallet-<WALLET_ID>"`. Na AWS, mensagens com o mesmo `MessageGroupId` são garantidamente entregues em ordem estrita de chegada, sem disputa paralela.
  2. O `SQSConsumerWorker` roda em segundo plano em uma goroutine contínua usando `sqs.ReceiveMessage` com Long Polling (espera de até 20 segundos para poupar CPU e custos de requisição).
  3. Ao receber o evento, o worker registra a mensagem na tabela `inbox_messages` para evitar reprocessamento (*Inbox Pattern*) e executa o débito de R$ 20,00 na carteira. O saldo é reduzido de R$ 620,00 para R$ 600,00.

#### Comando no Terminal
```bash
MSG_ID="sqs-manual-$(date +%s)"
EXT_TX_ID="ext-sqs-$(date +%s)"

AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test aws --region us-east-1 \
  --endpoint-url=http://localhost:4566 sqs send-message \
  --queue-url http://localhost:4566/000000000000/wager-transactions.fifo \
  --message-group-id "wallet-$WALLET_ID" \
  --message-deduplication-id "$MSG_ID" \
  --message-body '{
    "messageId": "'"$MSG_ID"'",
    "type": "WagerTransactionRequested",
    "occurredAt": "2026-09-15T21:00:00Z",
    "data": {
      "providerId": "provider-a",
      "externalTransactionId": "'"$EXT_TX_ID"'",
      "idempotencyKey": "provider-a:'"$EXT_TX_ID"'",
      "playerId": "'"$PLAYER_ID"'",
      "walletId": "'"$WALLET_ID"'",
      "roundId": "round-sqs-01",
      "gameId": "game-jungle-slots",
      "kind": "BET",
      "money": {"amount": "20.00", "currency": "BRL"}
    }
  }' > /dev/null

# Aguarda 1.5s para consumo e processamento pelo worker
sleep 1.5

# Consulta o saldo para confirmar débito de 620.00 para 600.00
curl -s -X GET http://localhost:8000/wallets/$WALLET_ID \
  -H "Authorization: Bearer $TOKEN_INTERNAL" | jq .balance
```

#### Resposta Esperada (`200 OK`)
```json
{
  "amount": "600.00",
  "currency": "BRL"
}
```

---

## 5. Etapa 3: Matriz de Resiliência e Casos de Borda (Unhappy Path)

---

### Cenário E1: Carteira Duplicada (409 Conflict)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Constraint de Banco Relacional `UNIQUE(player_id, currency)`.
- **Como Funciona por Baixo dos Panos:**
  O PostgreSQL mantém um índice B-Tree único cobrindo a tupla `(player_id, currency)`. Quando uma segunda tentativa de criação chega para o mesmo jogador na mesma moeda, o banco dispara o erro de integridade SQL `23505 (unique_violation)`. O repositório Go captura esse código de erro e o converte em `domain.ErrDuplicateWallet`, que a camada HTTP traduz para `409 Conflict`.
- **Por que isso é crítico:** Impede que um jogador tenha duas carteiras ativas na mesma moeda, o que fragmentaria o saldo e causaria falhas contábeis.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wallets \
  -H "Authorization: Bearer $TOKEN_INTERNAL" \
  -H "Content-Type: application/json" \
  -d '{
    "playerId": "'"$PLAYER_ID"'",
    "initialBalance": {
      "amount": "100.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`409 Conflict`)
```json
{
  "error": "wallet already exists for this player and currency"
}
```

---

### Cenário E2: Saldo Insuficiente (Rejeição Atômica Auditável)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Máquina de Estados Finita com Estado Terminal `REJECTED` + Resposta de Negócio HTTP 200.
- **Como Funciona por Baixo dos Panos:**
  Em arquiteturas financeiras de jogos (*iGaming*), saldo insuficiente **não é um erro técnico de servidor (não é 500 nem 404)**. É um desfecho legítimo de negócio!
  1. A tentativa de aposta de R$ 99.999,00 é submetida.
  2. O agregado constata que `wallet.Balance() < wager.Money()`.
  3. A transação transiciona para `Status: "REJECTED"` com `FailureCode: "INSUFFICIENT_FUNDS"`.
  4. Ela é **persistida no banco de dados e na outbox**, registrando formalmente a recusa.
  5. A API responde `200 OK` com o status `REJECTED` e o saldo atual intacto, permitindo que a interface do jogo mostre ao apostador uma mensagem clara: *"Saldo insuficiente para esta aposta"*.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-fail-balance-$(date +%s)" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-fail-balance-'"$(date +%s)"'",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "BET",
    "money": {
      "amount": "99999.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`200 OK`)
```json
{
  "transactionId": "a1f59fdc-63cd-4061-93cc-eefe097170be",
  "status": "REJECTED",
  "balance": {
    "amount": "600.00",
    "currency": "BRL"
  },
  "failureCode": "INSUFFICIENT_FUNDS",
  "idempotentReplay": false
}
```

---

### Cenário E3: Moeda Divergente (422 Unprocessable Entity)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Regra Invariante de Domínio no Value Object `Money`.
- **Como Funciona por Baixo dos Panos:**
  O sistema rejeita qualquer operação onde a moeda da transação (ex: `USD`) divirja da moeda nativa da carteira (`BRL`). A comparação `if wallet.Currency() != req.Currency()` é executada antes de qualquer chamada ao banco de dados, poupando I/O.
- **Por que isso é crítico:** Impede a mistura acidental de moedas sem câmbio prévio, prevenindo prejuízos financeiros graves de conversão de divisas.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-fail-currency-$(date +%s)" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-fail-currency-'"$(date +%s)"'",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "BET",
    "money": {
      "amount": "10.00",
      "currency": "USD"
    }
  }' | jq .
```

#### Resposta Esperada (`422 Unprocessable Entity`)
```json
{
  "error": "currency mismatch: operations require matching currencies"
}
```

---

### Cenário E4: Conflito de Idempotência / Payload Adulterado (409 Conflict)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Blindagem Anti-Colisão via Hash SHA-256.
- **Como Funciona por Baixo dos Panos:**
  1. A chave de idempotência `provider-a:tx-bet-001` já foi processada no passo 2.4 com o valor de **R$ 50,00**.
  2. Agora, o atacante ou um sistema com falha reenvia **a mesma chave**, porém alterando o valor da aposta para **R$ 99,00**.
  3. O hash SHA-256 calculado para o novo payload não bate com o hash original registrado no banco.
  4. O sistema detecta colisão de idempotência e aborta a requisição com `409 Conflict`.
- **Por que isso é crítico:** Impede ataques de fraude onde um agente malicioso tenta reutilizar o recibo de uma aposta pequena para confirmar uma aposta maior.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-bet-001" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-bet-001",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "BET",
    "money": {
      "amount": "99.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`409 Conflict`)
```json
{
  "error": "conflito: chave de idempotencia reutilizada com payload divergente"
}
```

---

### Cenário E5: Header Mandatório Ausente (400 Bad Request)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Middleware de Inspeção de Contrato HTTP em Go.
- **Como Funciona por Baixo dos Panos:**
  No início da função `ProcessTransaction` em `handlers.go`, a presença do cabeçalho HTTP `Idempotency-Key` é avaliada antes de efetuar o parsing do corpo JSON. Se a string for vazia ou conter apenas espaços em branco, a conexão é abortada imediatamente com código `400`.
- **Por que isso é crítico:** Protege a arquitetura distribuída contra clientes mal-implementados que não possuem suporte a garantias de idempotência.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-sem-header",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "BET",
    "money": {"amount": "10.00", "currency": "BRL"}
  }' | jq .
```

#### Resposta Esperada (`400 Bad Request`)
```json
{
  "error": "missing mandatory 'Idempotency-Key' header"
}
```

---

### Cenário E6: Falha de Autenticação / Token Inválido (401 Unauthorized)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Middleware de Autenticação JWT com Validação Criptográfica RS256.
- **Como Funciona por Baixo dos Panos:**
  O cabeçalho `Authorization: Bearer <token>` é interceptado pelo middleware de autenticação. Se o token não estiver presente, estiver expirado ou a assinatura não coincidir com a chave pública do Keycloak, o pipeline de execução é interrompido antes de atingir qualquer camada de negócio.
- **Por que isso é crítico:** Impede que agentes não autenticados executem movimentações financeiras na plataforma.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer token-falso-ou-expirado" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: prov:fake" \
  -d '{"kind":"BET"}' | jq .
```

#### Resposta Esperada (`401 Unauthorized`)
```json
{
  "error": "invalid or expired token"
}
```

---

### Cenário E7: Violação de Tenancy / Provedor Invasor (403 Forbidden)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Governança Multi-Tenant baseada nas Claims do JWT.
- **Como Funciona por Baixo dos Panos:**
  1. A requisição utiliza o token emitido para o **`provider-b`** (`TOKEN_PROV_B`).
  2. No entanto, o corpo do JSON tenta debitar uma transação identificando o `providerId: "provider-a"`.
  3. O middleware extrai a claim `azp` / `providerId` do JWT (`provider-b`) e compara com o `req.ProviderID` (`provider-a`).
  4. Ao constatar a divergência, a requisição é bloqueada imediatamente com status `403 Forbidden`.
- **Por que isso é crítico:** Garante o isolamento completo de tenants. O Cassino B jamais pode realizar débitos ou ter visibilidade sobre os jogadores e transações do Cassino A.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_B" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-tenant-attack" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-tenant-attack",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "BET",
    "money": {
      "amount": "10.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`403 Forbidden`)
```json
{
  "error": "forbidden: provider token does not match requested providerId"
}
```

---

### Cenário E8-A: Estorno Fora de Ordem (Chegada Precoce -> PENDING_REFERENCE)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Tolerância a Entrega Fora de Ordem (*Out-of-Order Resolution*) + `PendingReferenceWorker`.
- **Como Funciona por Baixo dos Panos:**
  Em redes distribuídas e mensageria assíncrona, um pacote de cancelamento (`REFUND` ou `ROLLBACK`) pode chegar alguns milissegundos **antes** da aposta original (`BET`) devido a rotas de rede divergentes ou latência.
  1. Se a API simplesmente rejeitasse com erro 404, o sistema ficaria em estado inconsistente.
  2. Em vez disso, a API aceita a transação no estado **`PENDING_REFERENCE`** (HTTP 200).
  3. O evento é gravado no banco sem alterar o saldo da carteira.
  4. O background worker `PendingReferenceWorker` roda a cada 500ms conferindo se a aposta original chegou. Assim que a aposta é processada, o worker resolve o estorno e atualiza o saldo automaticamente!

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-out-of-order-$(date +%s)" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-out-of-order-'"$(date +%s)"'",
    "referenceExternalTransactionId": "tx-aposta-que-ainda-nao-chegou-'"$(date +%s)"'",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "REFUND",
    "money": {
      "amount": "10.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`200 OK`)
```json
{
  "transactionId": "9e5c4120-d471-482a-9f5b-117c24a68123",
  "status": "PENDING_REFERENCE",
  "balance": {
    "amount": "600.00",
    "currency": "BRL"
  },
  "idempotentReplay": false
}
```

---

### Cenário E8-B: Estorno Duplicado de Aposta Já Revertida (422 Unprocessable)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Verificação de Reversão Única no Repositório SQL (`HasSuccessfulReversal`).
- **Como Funciona por Baixo dos Panos:**
  1. A aposta `tx-bet-001` já foi devidamente estornada com sucesso no passo 2.7.
  2. Se o provedor tentar enviar um **segundo estorno** (com uma chave de transação diferente, ex: `tx-refund-002`) apontando para a mesma `tx-bet-001`, o método `validateReversalReference` verifica o histórico no banco de dados.
  3. Ao detectar que a aposta já possui uma reversão confirmada, a operação é abortada retornando `domain.ErrReferenceAlreadyReversed`, mapeado para `422 Unprocessable Entity`.
- **Por que isso é crítico:** Impede a fraude de estorno duplicado (*Double Refund Fraud*), onde o jogador receberia o crédito de volta múltiplas vezes para uma única aposta perdida.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-refund-duplicado-$(date +%s)" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-refund-duplicado-'"$(date +%s)"'",
    "referenceExternalTransactionId": "tx-bet-001",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "REFUND",
    "money": {
      "amount": "50.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`422 Unprocessable Entity`)
```json
{
  "error": "reference transaction has already been reversed"
}
```

---

### Cenário E9: Violação da Imutabilidade do Ledger via SQL (Trigger de Banco)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Trigger PL/pgSQL `BEFORE UPDATE OR DELETE` no PostgreSQL.
- **Como Funciona por Baixo dos Panos:**
  A tabela `wallet_ledger_entries` possui uma trigger compilada diretamente no banco de dados:
  ```sql
  CREATE OR REPLACE FUNCTION fn_protect_ledger_immutability()
  RETURNS TRIGGER AS $$
  BEGIN
      RAISE EXCEPTION 'Operacao proibida: os lancamentos do ledger sao estritamente imutaveis e append-only.';
  END;
  $$ LANGUAGE plpgsql;

  CREATE TRIGGER trg_protect_ledger_entries
  BEFORE UPDATE OR DELETE ON wallet_ledger_entries
  FOR EACH ROW EXECUTE FUNCTION fn_protect_ledger_immutability();
  ```
  Mesmo que um DBA, invasor ou bug execute um comando SQL direto como `UPDATE` ou `DELETE` com permissões de `postgres` (superuser), o motor do banco cancela a operação instantaneamente.
- **Por que isso é crítico:** Assegura a integridade absoluta *Append-Only* do extrato contábil. Nenhum registro histórico pode ser apagado ou maquiado.

#### Comando no Terminal
```bash
# Tentativa de atualizar diretamente um registro do Ledger via psql no container
docker exec betting-postgres psql -U postgres -d betting_db -c \
  "UPDATE wallet_ledger_entries SET amount = 999999 WHERE wallet_id = '$WALLET_ID';"
```

#### Resposta Esperada (Exceção PL/pgSQL do PostgreSQL)
```text
ERROR:  Operacao proibida: os lancamentos do ledger sao estritamente imutaveis e append-only.
CONTEXT:  PL/pgSQL function fn_protect_ledger_immutability() line 3 at RAISE
```

---

### Cenário E10: Valor Zero ou Negativo Rejeitado (422 Unprocessable)

#### 📚 Fundamento Tecnológico
- **Tecnologia:** Validação de Domínio `domain.ErrZeroNotAllowed` e `domain.ErrNegativeMoney`.
- **Como Funciona por Baixo dos Panos:**
  Em jogos de aposta, zero só é aceito em transações do tipo `LOSS` (onde o saldo do prêmio é 0.00). Em transações ativas de movimentação (`BET`, `WIN`, `REFUND`), o valor monetário deve ser estritamente maior que zero centavos (`cents > 0`). A camada de domínio rejeita a operação retornando `422 Unprocessable Entity`.
- **Por que isso é crítico:** Impede a poluição inútil do banco de dados com transações vazias e bloqueia explorações de overflow com valores monetários negativos.

#### Comando no Terminal
```bash
curl -s -X POST http://localhost:8000/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:tx-zero-fail-$(date +%s)" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-zero-fail-'"$(date +%s)"'",
    "playerId": "'"$PLAYER_ID"'",
    "walletId": "'"$WALLET_ID"'",
    "roundId": "round-slots-01",
    "gameId": "game-jungle-slots",
    "kind": "BET",
    "money": {
      "amount": "0.00",
      "currency": "BRL"
    }
  }' | jq .
```

#### Resposta Esperada (`422 Unprocessable Entity`)
```json
{
  "error": "zero amount is not allowed for this operation"
}
```

---

## 6. Tabela Resumo de Códigos HTTP e Tratamento Arquitetural

| Código HTTP | Erro de Domínio / Descrição | Tecnologia de Barreira | Causa Típica & Prevenção no Mundo Real |
|---|---|---|---|
| **`400 Bad Request`** | `missing mandatory 'Idempotency-Key' header` | Middleware HTTP Chi | O cliente não enviou a chave de idempotência obrigatória. Previne operações cegas sem rastreabilidade de replay. |
| **`401 Unauthorized`** | `invalid or expired token` | Keycloak / OIDC RS256 | Token JWT ausente, assinado com chave inválida ou expirado. Previne acesso de agentes não autorizados. |
| **`403 Forbidden`** | `forbidden: provider token does not match requested providerId` | Claims Context Validator | Token de um provedor tentando realizar mutação em dados de outro provedor. Previne quebra de isolamento multi-tenant. |
| **`404 Not Found`** | `wallet not found` / `transação não encontrada` | Repositório PostgreSQL | A carteira ou transação solicitada não existe na base de dados. |
| **`409 Conflict`** | `wallet already exists for this player and currency` | Constraint `UNIQUE(player_id, currency)` | Tentativa de abrir uma segunda carteira para o mesmo jogador na mesma moeda. |
| **`409 Conflict`** | `conflito: chave de idempotencia reutilizada com payload divergente` | SHA-256 Canonical Check | Reutilização da mesma chave com valores ou parâmetros modificados. Previne colisão e fraude de replay. |
| **`422 Unprocessable`** | `currency mismatch: operations require matching currencies` | Value Object `Money` | Tentativa de debitar ou creditar moeda estrangeira (ex: USD) em carteira BRL sem conversão de câmbio. |
| **`200 OK` (Rejeição)**| `Status: REJECTED`, `FailureCode: INSUFFICIENT_FUNDS` | Máquina de Estados Finita | O jogador não possui saldo suficiente para apostar. É um desfecho legítimo de negócio auditável gravado no banco, e não uma falha de servidor. |
| **`200 OK` (Pendente)**| `Status: PENDING_REFERENCE` | Resolução Fora de Ordem | Um estorno chegou antes da aposta original na rede distribuída. O sistema acolhe o evento para conciliação assíncrona pelo worker. |

---

## 7. Cockpit Visual Interativo: Jungle Slots 1987 (WebAssembly & Telemetria em Tempo Real)

Além de executar os testes via terminal ou Postman, tu podes auditar visualmente 100% dos cenários deste guia através do simulador de fliperama retrô **Jungle Slots 1987**, desenvolvido com motor em **Go WebAssembly (WASM)** e áudio chiptune 8-bit sintetizado:

- 🖥️ **Acesso Local (Docker / Engine Stateful Completa):** [http://localhost:8000/app/](http://localhost:8000/app/)
- 🌐 **Acesso Online (Portfólio na Vercel):** [https://jungle-slots-1987.vercel.app](https://jungle-slots-1987.vercel.app)
- 📱 **Acesso Mobile:** Compatibilidade total com smartphones e tablets touchscreen (iPhone / Android) com alvos de toque otimizados e vibração háptica.

### Mapeamento Direto entre o Cockpit e os Cenários deste Guia

1. **Giro da Aposta (`🎰 RODAR O CARIMBÓ!`):**
   - Dispara o **Cenário 2.4 (BET)**. No Docker local, executa o débito com `SELECT ... FOR UPDATE` no PostgreSQL e insere o registro imutável no Ledger contábil.
2. **Replay Idempotente (`🔁 REPLAY IDEMPOTENTE`):**
   - Dispara o **Cenário 2.5 (Idempotência)**. Reenvia a exata mesma chave (`Idempotency-Key`) e payload. O sistema comprova que o saldo permanece inalterado e nenhum débito duplicado ocorre.
3. **Estorno Regulatório (`↩️ ESTORNAR APOSTA`):**
   - Dispara o **Cenário 2.7 (REFUND)**. Devolve o montante integral para a carteira. Um segundo clique consecutivo é bloqueado pelo botão e pela engine (Cenário E8-B - Anti-Double Refund).
4. **Aba de Auditoria & Reconciliação Contábil (`⚖️ AUDITORIA`):**
   - Dispara o **Cenário 2.8 (Reconciliação Zero-Divergence)**. Executa a prova matemática no Livro-Razão e exibe o badge verde de conformidade instantânea (`Diferença: R$ 0,00`).
5. **Aba de Casos de Borda (`🧪 UNHAPPY PATHS`):**
   - Executa interativamente os testes de saldo insuficiente (E2), invasão de tenant (E7), moeda estrangeira USD (E3), valores negativos (E10), chaves conflitantes (E4) e disputa de concorrência com 2 goroutines simultâneas, renderizando os payloads e cabeçalhos HTTP no monitor CRT.

