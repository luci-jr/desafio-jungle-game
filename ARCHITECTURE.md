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
- **Hash canônico:** a mesma chave só é aceita com o mesmo payload de negócio; divergências retornam `409 Conflict`.
- **Double-check sob o lock `FOR UPDATE`:** a aplicação inicia a transação, bloqueia exclusivamente a carteira envolvida e então consulta `idempotency_key` e `(provider_id, external_transaction_id)`. Das 50 requisições simultâneas, a primeira confirma a mutação; as demais obtêm o lock em seguida, enxergam o registro já confirmado e retornam `idempotentReplay: true`, sem novo débito.
- **Constraints únicas no PostgreSQL:**
  ```sql
  CONSTRAINT uq_transactions_provider_external UNIQUE (provider_id, external_transaction_id),
  CONSTRAINT uq_transactions_idempotency UNIQUE (idempotency_key)
  ```
  Elas constituem a última barreira de integridade caso uma tentativa concorrente alcance o `INSERT`.

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

---

## 11. Cockpit Web Embutido (`Jungle Slots 1987`), WebAssembly e Entrega Self-Hosted

### 11.1. Distribuição Single-Binary com `//go:embed`
- **Decisão:** Os artefatos estáticos da interface web (HTML, CSS, JavaScript, áudio e o binário WebAssembly `game.wasm`) são embutidos diretamente dentro do executável compilado de Go utilizando `embed.FS` ([`internal/infrastructure/http/web/ui.go`](internal/infrastructure/http/web/ui.go)).
- **Racional:** Elimina a necessidade de servidores web externos (como Nginx) para servir o frontend, suprime etapas adicionais de build no ambiente de execução e previne problemas de CORS ou rotas quebradas. Um único container ou binário disponibiliza a API e a interface de testes visual imediatamente em `http://localhost:8000/app/`.

### 11.2. Motor em WebAssembly (Go WASM)
- O motor de geração e regras probabilísticas do jogo roda em **WebAssembly** compilado diretamente a partir de código Go nativo (`cmd/wasm/main.go`), operando no navegador via `wasm_exec.js`.
- Demonstra a versatilidade de Go tanto para microserviços de alto rendimento no servidor quanto para binários de execução rápida em client-side.

### 11.3. Otimização Touchscreen & Responsividade Mobile
- **Touch-Action & Ergonomia:** Interface adaptada para smartphones e tablets com botões que respeitam as zonas de alcance do polegar (touch targets de 44px a 56px), `touch-action: manipulation` para suprimir delays de clique e barra flutuante sticky de navegação rápida entre o Fliperama e a Auditoria.
- **Haptic Engine:** Integração com a API de vibração (`navigator.vibrate`) para feedback tátil realista em apostas, vitórias e erros no celular.

### 11.4. Racional de Entrega Híbrida: Docker Stateful vs. Portfólio Web na Vercel
- **A Engine Stateful (Padrão Ouro para Avaliadores Técnicos):**
  - O desafio exige persistência relacional transacional (PostgreSQL 16 com locks `SELECT ... FOR UPDATE` e triggers PL/pgSQL), autenticação OIDC corporativa (Keycloak 24 com JWKS) e mensageria assíncrona (AWS SQS FIFO com workers contínuos em background).
  - Esta infraestrutura roda de forma 100% autossuficiente via `docker compose up -d`, entregando a API e o cockpit web embutido (`//go:embed`) em `http://localhost:8000/app/`.
- **O Modo Portfólio WebAssembly na Nuvem ([jungle-slots-1987.vercel.app](https://jungle-slots-1987.vercel.app)):**
  - Para permitir que recrutadores, gestores e a comunidade acessem a experiência interativa em qualquer dispositivo móvel ou desktop sem precisar instalar Docker localmente, o cockpit possui detecção automática de ambiente.
  - Ao rodar na Vercel, o binário WebAssembly (`game.wasm`) ativa o **Modo Demonstração Portfólio**: executa toda a lógica probabilística, livro-razão contábil, replay idempotente e prova de reconciliação matemática diretamente em memória via máquina de estados compilada em Go.
  - Isso une o melhor de dois mundos: fidelidade transacional corporativa máxima na máquina do recrutador e vitrine viva de portfólio acessível globalmente.

---

## 12. Interpretações Adotadas, Limitações e Trabalho Futuro / Não Concluído

Em estrita conformidade com os requisitos de entrega da Seção 15 do edital (*"Explicite limitações, interpretações adotadas e trabalho não concluído"*), este capítulo formaliza as decisões de contorno, hipóteses de projeto e o roadmap de evolução técnica da solução.

### 12.1. Interpretações Adotadas
1. **Moeda e Operação em BRL:**
   - O value object `domain.Money` foi projetado com suporte a qualquer código de moeda alfabético de 3 letras da norma ISO 4217, possuindo validações e testes unitários de incompatibilidade em operações aritméticas entre moedas distintas.
   - Para os fluxos de integração e cenários principais de aposta do edital, adotou-se o Real Brasileiro (`BRL`) como moeda padrão do ambiente de testes e da carteira inicial.
2. **Resolução de `PENDING_REFERENCE` e TTL de Expiração:**
   - Quando uma reversão (`REFUND` ou `ROLLBACK`) chega antes da aposta que ela referencia, o sistema persiste o registro em estado `PENDING_REFERENCE` e publica o evento correspondente na Outbox.
   - Fixou-se a estratégia de resolução com TTL de 60 segundos (ou até 5 ciclos de retry do worker assíncrono com backoff exponencial). Esgotado esse prazo sem a chegada da transação referenciada, a operação é finalizada como `REJECTED` com o código estável `REFERENCE_NOT_FOUND` e produz o evento `WagerTransactionRejected`.
3. **Normalização e Hash Determinístico de Idempotência:**
   - O cálculo do hash SHA-256 é restrito aos campos semânticos de negócio: `(providerId, externalTransactionId, playerId, walletId, roundId, gameId, kind, amount, currency, referenceExternalTransactionId)`.
   - Headers de transporte (como `User-Agent`, `Authorization`, `X-Forwarded-For`) e IDs de mensagem de transporte do broker foram intencionalmente excluídos do hash. Essa interpretação garante que a mesma intenção de negócio, seja enviada via HTTP ou via SQS FIFO, gere rigorosamente o mesmo hash e impeça duplicações ou detecte conflitos (`409 Conflict`).
4. **Fechamento Síncrono de Operações sem Dependências:**
   - Para operações sem referências externas pendentes (`BET`, `WIN`, `LOSS`), o processamento e o commit do estado terminal (`PROCESSED` ou `REJECTED`) ocorrem de forma atômica e síncrona dentro da mesma transação SQL, sem necessidade de um commit intermediário de aceite prévio, otimizando o throughput do banco.

### 12.2. Limitações da Solução Atual
1. **Emulação Local de Mensageria (LocalStack):**
   - No ambiente de desenvolvimento e testes autossuficiente via Docker Compose, o AWS SQS FIFO é emulado através do LocalStack 3.7. Embora atenda a todas as semânticas de filas FIFO (`.fifo`), grupos de mensagens (`MessageGroupId`) e deduplicação (`MessageDeduplicationId`), as garantias de latência e escalabilidade massiva de conexões dependem do container emulado no host em vez dos clusters globais gerenciados da AWS.
2. **Ausência de Módulo de Conversão Cambial Dinâmica (Forex):**
   - O sistema bloqueia movimentações financeiras cuja moeda seja diferente da moeda da carteira (retornando `HTTP 422 Unprocessable Entity`). Não há conversão cambial em tempo real entre diferentes moedas, mantendo a responsabilidade de conversão com o provedor externo antes da submissão da transação.
3. **Retenção e Crescimento da Tabela de Inbox:**
   - A tabela `inbox_messages` é append-only para auditoria e deduplicação estrita de consumo SQS. Para operação contínua de longo prazo em produção corporativa de alta escala, é recomendada a introdução de uma rotina de particionamento mensal de tabela (*table partitioning*) ou job de expurgo após o período máximo de retenção de mensagens do SQS (14 dias).

### 12.3. Trabalho Futuro e Extensões Opcionais (Diferenciais do Edital)
1. **Distributed Tracing com OpenTelemetry (OTel):**
   - A aplicação atualmente produz logs JSON estruturados com todos os identificadores de correlação (`correlationId`, `messageId`, `transactionId`, `walletId`, `providerId`) e health checks `/health/live` e `/health/ready`.
   - Como evolução opcional descrita no edital, pode ser adicionado o SDK do OpenTelemetry com exportador OTLP para rastreamento distribuído de spans entre HTTP, workers e banco via Jaeger/Tempo.
2. **Contabilidade em Partidas Dobradas (Double-Entry Ledger):**
   - O livro-razão atual utiliza modelo append-only imutável com prova matemática de reconciliação contábil (`POST /wallets/{walletId}/reconciliation`), suficiente para auditar o saldo do jogador.
   - Um trabalho complementar futuro é expandir o schema para partidas dobradas clássicas, debitando a conta do jogador e creditando simultaneamente a conta de contrapartida da casa/provedor (*House Account*).
3. **Métricas Avançadas em Formato Prometheus:**
   - Exposição de um endpoint `/metrics` formatado para scraping do Prometheus, com percentis de latência (p50, p95, p99), histogramas de contenda de locks e medidores de atraso de publicação da outbox (*outbox lag*).
