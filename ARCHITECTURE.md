# 🏛️ Decisões Arquiteturais e de Design — ARCHITECTURE.md

Este documento detalha formalmente as escolhas de arquitetura, modelagem de dados, concorrência, idempotência e resiliência adotadas no serviço de processamento de apostas.

---

## 1. Precisão Monetária (`Money` Value Object)

### 1.1. Representação e Eliminação de Ponto Flutuante
- **Decisão:** O tipo `domain.Money` é modelado como um **Value Object imutável** que armazena internamente o valor monetário em **centavos inteiros (`int64`)** e o código de moeda ISO 4217 de 3 letras maiúsculas.
- **Racional:** Números em ponto flutuante (`float32` ou `float64`) utilizam representação binária IEEE 754, sujeita a dízimas periódicas que provocam perda de precisão em centavos (ex: `0.1 + 0.2 = 0.30000000000000004`). Em finanças, isso é proibido.
- **Limites e Proteção contra Overflow:** `int64` suporta até $9.223.372.036.854.775.807$ centavos ($\approx 92$ quatrilhões de reais), cobrindo amplamente o volume transacional sem risco de estouro. As operações aritméticas (`Add`, `Sub`, `Negate`) verificam overflow antes da atribuição.
- **Parsing Estrito:** Parsing manual de strings decimais (formato `"25.00"`). Não permite notação científica, `NaN`, `Infinity`, números sem ponto ou com mais de 2 casas decimais.

### 1.2. Mapeamento no Banco de Dados
- Na base de dados PostgreSQL, os valores monetários são persistidos no schema como `BIGINT` representando centavos, garantindo compatibilidade estrita, indexação de alta velocidade e integridade com a regra de negócio `CHECK (amount >= 0)`.

---

## 2. Controle de Concorrência e Delimitação de Transações (Locking)

### 2.1. Escopo por Carteira e Ausência de Locks Globais
- **Decisão:** A concorrência é coordenada a nível de **carteira individual** através de bloqueio pessimista relacional no PostgreSQL:
  ```sql
  SELECT id, balance, version ... FROM wallets WHERE id = $1 FOR UPDATE
  ```
- **Racional:**
  - Locks globais ou mutexes em memória de aplicação violam a escalabilidade horizontal de múltiplas réplicas (instâncias) e reduzem o throughput global.
  - Com `SELECT ... FOR UPDATE` indexado por `id`, transações de carteiras diferentes progridem de forma 100% paralela e concorrente sem contenda.
  - Elimina **Lost Updates**: duas requisições concorrentes sobre a mesma carteira são estritamente enfileiradas pelo motor de transação ACID do PostgreSQL.

### 2.2. Disputa de Apostas Simultâneas (Caso dos 80.00 BRL sobre 100.00 BRL)
- Quando duas apostas de 80.00 chegam simultaneamente em uma carteira de 100.00:
  1. A primeira transação adquire o lock `FOR UPDATE`, debita 80.00 (saldo vai para 20.00), grava o lançamento de débito no ledger, a transação `PROCESSED` e comita.
  2. A segunda transação é liberada pelo lock do Postgres. Ao ler o saldo atualizado (20.00), detecta saldo insuficiente (`less := balance < 80.00`).
  3. Transiciona para `REJECTED` com o código estável `INSUFFICIENT_FUNDS`, grava a transação rejeitada para auditoria e comita sem gerar débito no ledger.
  4. Resultado comprovado nos testes automatizados: saldo final de 20.00 BRL, 1 aposta processada, 1 rejeitada, 1 lançamento no ledger.

---

## 3. Idempotência Persistente e Detecção de Conflito

### 3.1. Hash Canônico Determinístico
- Um hash SHA-256 é calculado a partir de um DTO ordenado contendo exclusivamente os campos de negócio:
  `(providerId, externalTransactionId, playerId, walletId, roundId, gameId, kind, amount, currency, referenceExternalTransactionId)`.
- Metadados de transporte (headers HTTP, IDs de mensagem SQS) não entram no hash.

### 3.2. Mecanismo de Idempotência e Double-Check sob Lock
- **Primeiro Nível (Leitura Otimista):** Antes de abrir transação de escrita, busca no banco por `idempotency_key` ou `(provider_id, external_transaction_id)`. Se já existe e o hash coincide, devolve imediatamente o resultado persistido com `idempotentReplay: true`. Se o hash diverge, devolve `409 Conflict`.
- **Segundo Nível (Double-Check sob o Lock `FOR UPDATE`):** Para 50 requisições simultâneas que chegam no mesmo milissegundo, todas passarão pelo primeiro nível antes da primeira comitar. Ao adquirir o lock da carteira, a aplicação realiza um double-check dentro da transação `dbTx`. As 49 requisições concorrentes identificam que a transação foi recém-comitada pelo primeiro worker e retornam `idempotentReplay: true` sem executar débito duplo.
- **Terceiro Nível (Constraints Únicas no PostgreSQL):**
  ```sql
  CONSTRAINT uq_transactions_provider_external UNIQUE (provider_id, external_transaction_id),
  CONSTRAINT uq_transactions_idempotency UNIQUE (idempotency_key)
  ```
  Se houver colisão de commit concorrente, o PostgreSQL rejeita com erro `23505 (unique_violation)`. A aplicação captura o erro, consulta a transação persistida e retorna o replay idêntico com segurança.

---

## 4. Livro-Razão (Ledger) Estritamente Imutável e Append-Only

### 4.1. Invariante Contábil
- Toda entrada no ledger (`WalletLedgerEntry`) valida matematicamente no domínio:
  - Crédito: `balanceAfter = balanceBefore + amount`
  - Débito: `balanceAfter = balanceBefore - amount`
- O lançamento armazena `id`, `wallet_id`, `transaction_id`, `direction`, `amount`, `balance_before`, `balance_after` e `created_at`.
- `LOSS` e transações rejeitadas não geram lançamentos no ledger.

### 4.2. Blindagem de Imutabilidade no Schema
- Um gatilho (*trigger*) em PL/pgSQL bloqueia qualquer tentativa de alteração (`UPDATE`) ou exclusão (`DELETE`) na tabela `wallet_ledger_entries`:
  ```sql
  CREATE OR REPLACE FUNCTION fn_protect_ledger_immutability()
  RETURNS TRIGGER AS $$
  BEGIN
      RAISE EXCEPTION 'Operacao proibida: os lancamentos do ledger sao estritamente imutaveis e append-only.';
  END;
  $$ LANGUAGE plpgsql;
  ```

---

## 5. Reconciliação Contábil (`/reconciliation`)

- O endpoint `POST /wallets/{walletId}/reconciliation` reconstrói o saldo a partir da soma dos lançamentos do ledger:
  $$\text{CalculatedBalance} = \sum \text{Credits} - \sum \text{Debits}$$
- Compara com o `balance` gravado na tabela `wallets` e valida a consistência (`difference = stored - calculated = 0`).
- A reconciliação é estritamente somente leitura; ela audita e reporta divergências sem alterar estados.

---

## 6. Reversões e Resolução Fora de Ordem (`PendingReferenceWorker`)

### 6.1. Reversões (`REFUND` e `ROLLBACK`)
- `REFUND`: Devolve integralmente o valor de uma `BET` processada como crédito.
- `ROLLBACK`: Desfaz o movimento original (desfaz aposta creditando o valor; desfaz ganho debitando o valor). Se o saldo for insuficiente para debitar um ganho revertido, rejeita com `ROLLBACK_INSUFFICIENT_FUNDS`.

### 6.2. Tratamento de Chegada Fora de Ordem
- Em sistemas distribuídos, uma reversão pode chegar antes da aposta original devido a roteamento ou retentativas de rede.
- Quando a referência não é encontrada, a operação não falha imediatamente:
  1. A transação é salva no banco em estado `PENDING_REFERENCE`.
  2. Um evento `WagerTransactionPendingReference` é gravado na Outbox.
  3. O `PendingReferenceWorker` varre periodicamente transações pendentes de referência.
  4. Assim que a transação original é confirmada como `PROCESSED`, o worker resolve a reversão, atualiza o saldo da carteira, cria o lançamento no ledger e transiciona para `PROCESSED`.
  5. Se expirar o TTL (1 minuto), a reversão é finalizada como `REJECTED` com `failureCode: "REFERENCE_NOT_FOUND"`.

---

## 7. Mensageria SQS FIFO e Inbox Pattern

- **Fila Principal:** `wager-transactions.fifo` com `ContentBasedDeduplication=false`.
- **Fila Morta (DLQ):** `wager-transactions-dlq.fifo` com Redrive Policy para 3 tentativas máximas.
- **Agrupamento (`MessageGroupId`):** Definido como `"wallet-" + walletId`, garantindo ordenação estrita por carteira sem travar carteiras diferentes.
- **Inbox Pattern:** Para evitar processamento duplo em reentregas da fila, a tabela `inbox_messages` registra `(consumer_name, message_id)`. Se a mensagem já foi consumida, é descartada da fila sem reaplicação financeira.

---

## 8. Transactional Outbox Pattern

- Todas as mutações de domínio (saldo da carteira, transação financeira, ledger e inbox) compartilham a **mesma transação SQL** no PostgreSQL.
- Na mesma transação, os eventos de integração são persistidos na tabela `outbox_events` como snapshots JSON imutáveis com status `PENDING`.
- O `OutboxPublisherWorker` realiza a leitura concorrente utilizando:
  ```sql
  SELECT ... FROM outbox_events WHERE status = 'PENDING' FOR UPDATE SKIP LOCKED
  ```
- Garante que múltiplas instâncias da aplicação possam publicar eventos simultaneamente sem lock blocking nem publicação duplicada.
- A publicação é feita na fila FIFO `wager-events.fifo` do SQS. O `eventId` é usado como
  `MessageDeduplicationId` e o agregado como `MessageGroupId`.
- Antes de publicar, o worker grava uma reserva persistente (`claim_token` e `claim_until`).
  Se uma instância cair, outra pode assumir o registro depois do vencimento da reserva.
- O evento só é marcado como `PUBLISHED` depois que o `SendMessage` retorna sucesso.

---

## 9. Autenticação OIDC e Isolamento de Provedores

- Integração nativa com **Keycloak** usando fluxo `client_credentials` e validação de tokens JWT RS256 via chaves públicas recuperadas do endpoint JWKS (`/certs`).
- **Isolamento de Provedores:** O middleware HTTP extrai a claim `providerId` do token autenticado e valida que o provedor autenticado só acesse e envie transações de seu próprio `providerId`. Tentativas de acesso cruzado resultam imediatamente em `403 Forbidden`.
- As rotas de carteira são restritas ao client interno `internal-service`, cuja claim é
  `providerId=admin`. Clientes de provedores não podem criar, consultar ou reconciliar carteiras.

---

## 10. Composição com Uber Fx e Graceful Shutdown

- A aplicação Go utiliza **Uber Fx** (`go.uber.org/fx`) para injeção de dependências declarativa baseada em construtores limpos.
- **Ciclo de Vida (`fx.Lifecycle`):**
  - **OnStart:** Aplica migrations SQL no PostgreSQL, inicia workers assíncronos (SQS Consumer, Outbox Publisher, Pending Reference Worker) em goroutines gerenciadas por context e sobe o servidor HTTP na porta 8000.
  - **OnStop:** Cancela os contextos dos workers assíncronos, encerra o servidor HTTP aguardando a conclusão das requisições ativas (`server.Shutdown`) e fecha o pool de conexões com o PostgreSQL (`pool.Close()`).
