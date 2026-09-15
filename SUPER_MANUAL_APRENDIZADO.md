# 🎓 Super Manual de Aprendizado Passo a Passo — Engine Distribuída de Apostas em Go

> **Autor:** Lucivaldo Junior (com tutoria e mentoria sênior do Lucy)
> **Objetivo:** Guia definitivo de estudo, compreensão conceitual e preparação para defesa técnica em entrevistas de desenvolvimento Back-end Go (iGaming / Finanças / Sistemas Concorrentes).

---

## 📌 Sumário
1. [Visão Geral do Desafio: O que é uma Engine de Apostas?](#1-visão-geral-do-desafio)
2. [A Estrutura do Projeto (Standard Go Layout + Clean Architecture)](#2-a-estrutura-do-projeto)
3. [Dinheiro sem Ponto Flutuante: O Value Object `Money`](#3-dinheiro-sem-ponto-flutuante-o-value-object-money)
4. [Entidades e Agregados: `Wallet`, `WagerTransaction` e `WalletLedgerEntry`](#4-entidades-e-agregados)
5. [Concorrência no Banco vs. Memória: O `SELECT ... FOR UPDATE`](#5-concorrência-no-banco-vs-memória)
6. [Idempotência em Três Níveis e a Batalha dos 50 Requests Concorrentes](#6-idempotência-em-três-níveis)
7. [O Padrão Transactional Outbox e a Prevenção do Dual-Write](#7-o-padrão-transactional-outbox)
8. [Mensageria SQS FIFO, Inbox Pattern e Resolução Fora de Ordem](#8-mensageria-sqs-fifo-e-inbox-pattern)
9. [Uber Fx: Injeção de Dependências e Ciclo de Vida Gracioso](#9-uber-fx-injeção-de-dependências)
10. [Segurança OIDC (Keycloak) e Isolamento entre Provedores](#10-segurança-oidc-e-isolamento-de-provedores)
11. [Roteiro de Defesa na Entrevista: Perguntas Críticas e Respostas Prontas](#11-roteiro-de-defesa-na-entrevista)

---

## 1. Visão Geral do Desafio

No ecossistema de apostas online (*iGaming* ou *Sportsbook*), múltiplos provedores de jogos (ex: estúdios de cassino, roleta, caça-níqueis como *Fortune Tiger*) conectam-se à plataforma central.

A cada giro ou rodada:
1. O jogador faz uma aposta (`BET`): o dinheiro sai da carteira.
2. O jogador ganha (`WIN`): o prêmio entra na carteira.
3. O jogador perde (`LOSS`): o saldo não muda, mas a rodada deve ser auditada.
4. Ocorre um cancelamento (`REFUND`): uma aposta cancelada é estornada.
5. Ocorre uma falha técnica no provedor (`ROLLBACK`): desfaz a operação anterior.

### O Problema do Mundo Real:
Em ambientes de alta concorrência com milhões de usuários e redes instáveis:
- A rede cai e o provedor reenvia a mesma aposta 5 vezes seguidas. Se tu debitar 5 vezes, o jogador é roubado.
- Um jogador com R$ 100,00 abre duas abas no celular e clica simultaneamente em duas apostas de R$ 80,00. Se teu sistema for lento, ambas passam e a carteira fica com saldo negativo de -R$ 60,00.
- A internet do provedor atrasa a aposta, mas envia um cancelamento logo em seguida. O `REFUND` chega ANTES do `BET` existir no teu banco.

**Nossa missão neste projeto:** Resolver 100% desses problemas com garantias matemáticas, ACID e distribuídas.

---

## 2. A Estrutura do Projeto

Seguimos rigorosamente o **Standard Go Project Layout**:

```
backend-challenge-go/
├── cmd/
│   └── api/
│       └── main.go              # Ponto de entrada (Fx Bootstrap)
├── internal/
│   ├── domain/                  # Lógica Pura de Domínio (Zero dependências externas)
│   │   ├── money.go             # Value Object Money (int64 centavos)
│   │   ├── wallet.go            # Agregado Wallet (Saldo, versão)
│   │   ├── transaction.go       # Transações (Máquina de estados)
│   │   ├── ledger.go            # Livro-razão contábil (Imutável)
│   │   ├── events.go            # Envelopes de eventos da Outbox
│   │   └── errors.go            # Erros sentinela tipados
│   ├── application/             # Casos de Uso (Orquestração de negócio)
│   │   ├── wallet_service.go    # Abertura e consulta de carteira
│   │   ├── wager_service.go     # Processamento atômico de apostas
│   │   └── reconciliation_service.go # Auditoria contábil
│   ├── infrastructure/          # Detalhes Técnicos e Adaptadores
│   │   ├── auth/                # Validação de JWT/OIDC com JWKS
│   │   ├── config/              # Leitura de variáveis (.env)
│   │   ├── database/            # pgx/v5 Pool e Repositórios SQL
│   │   ├── http/                # Router Chi, Handlers e Middlewares
│   │   └── messaging/           # Clientes SQS, Consumer, Outbox Publisher
│   └── module.go                # Módulo de composição do Uber Fx
├── migrations/                  # Scripts SQL versionados (Up/Down)
├── tests/                       # Testes de integração concorrentes
├── docker-compose.yml           # Postgres, LocalStack, Keycloak e API
├── Dockerfile                   # Build multi-stage enxuto em Alpine
├── ARCHITECTURE.md              # Decisões formais de arquitetura
└── README.md                    # Manual operacional de execução
```

**Regra de Ouro:** A pasta `internal/domain` NUNCA importa pacotes de banco (`pgx`), HTTP (`chi`) ou AWS (`sqs`). O domínio é puro Go.

---

## 3. Dinheiro sem Ponto Flutuante: O Value Object `Money`

### Por que NUNCA usar `float` para dinheiro?
Em Go (e na maioria das linguagens), `float64` implementa o padrão binário IEEE 754. Certas frações decimais (como `0.1` ou `0.01`) não possuem representação binária finita exata.
Ao somar floats seguidamente, acumulam-se erros de arredondamento:
```go
var f float64 = 0.1 + 0.2
fmt.Println(f == 0.3) // Retorna FALSE! f vale 0.30000000000000004
```
Em um cassino com 10 milhões de giros por dia, esses microléssimos de centavos geram rombos contábeis ou processos judiciais.

### Nossa Solução: Centavos em `int64`
Transformamos R$ 25,00 em `2500` centavos inteiros (`int64`).
- `int64` suporta até 9 quatrilhões de reais.
- Não perde um único centavo.
- O parsing é feito manualmente em `internal/domain/money.go`:
  - Valida se contém exatamente duas casas decimais após o ponto (`"25.00"`).
  - Rejeita notação científica (`"1e5"`), letras (`"abc"`), `NaN` e `Infinity`.
  - Checa overflow antes de qualquer soma ou subtração.

---

## 4. Entidades e Agregados

### 4.1. `Wallet` (A Raiz do Agregado)
A carteira controla o saldo e a versão:
- Cada débito ou crédito incrementa a versão (`version++`).
- **Invariante:** O saldo da carteira JAMAIS pode ficar negativo (`balance >= 0`).

### 4.2. `WalletLedgerEntry` (O Livro-Razão)
Em finanças corporativas, **nunca se altera um saldo sem registrar a contrapartida**.
- Cada débito ou crédito gera uma linha obrigatória na tabela `wallet_ledger_entries`.
- Armazena: `saldo_anterior`, `valor`, `saldo_posterior`, `direção` (`DEBIT` ou `CREDIT`).
- **Equação Matemática Inegociável:**
  $$\text{balanceAfter} = \text{balanceBefore} \pm \text{amount}$$
- **Imutabilidade Absoluta:** O banco de dados possui um *trigger* SQL que proíbe expressamente `UPDATE` e `DELETE` no ledger. O ledger é estritamente *append-only*.

### 4.3. `WagerTransaction` (A Máquina de Estados)
A transação inicia em `PENDING`.
- Transições válidas:
  - `PENDING` $\to$ `PROCESSED` (Concluída com sucesso)
  - `PENDING` $\to$ `PENDING_REFERENCE` (Aguardando referência chegar)
  - `PENDING` $\to$ `REJECTED` (Rejeitada por regra de negócio: ex. saldo insuficiente)
  - `PENDING_REFERENCE` $\to$ `PROCESSED` (Referência chegou e foi resolvida)
  - `PENDING_REFERENCE` $\to$ `REJECTED` (TTL expirou)
- Uma transação que atingiu estado terminal (`PROCESSED` ou `REJECTED`) **NUNCA** pode ser reprocessada.

---

## 5. Concorrência no Banco vs. Memória

### Por que não usar Mutex (`sync.Mutex`) em Go?
Se tu usares um Mutex em memória Go (`mu.Lock()`), ele só protege a concorrência **dentro daquela instância específica**.
Se a aplicação rodar em 3 containers no Docker Swarm ou Kubernetes, cada container terá seu próprio Mutex. As requisições simultâneas cairão em containers diferentes e ocorrerá débito duplo no banco de dados!

### A Solução Sênior: Bloqueio Pessimista no PostgreSQL
Usamos a garantia ACID do próprio banco relacional:
```sql
SELECT id, player_id, currency, balance, version
FROM wallets
WHERE id = $1
FOR UPDATE;
```
1. Quando uma requisição executa `SELECT ... FOR UPDATE`, o PostgreSQL coloca uma trava exclusiva na linha daquela carteira específica.
2. Nenhuma outra transação pode ler ou alterar essa carteira até que a primeira faça `COMMIT` ou `ROLLBACK`.
3. Carteiras de jogadores diferentes continuam rodando 100% em paralelo, garantindo throughput altíssimo.

---

## 6. Idempotência em Três Níveis

A idempotência garante que se um cliente enviar a mesma aposta 50 vezes, ela será cobrada **apenas uma vez**, e as outras 49 respostas receberão exatamente o mesmo resultado original com `idempotentReplay: true`.

Construímos uma proteção em 3 camadas à prova de falhas:

### Nível 1: Hash Canônico SHA-256
Gera um hash criptográfico a partir do JSON ordenado dos campos de negócio. Se o cliente enviar a mesma `Idempotency-Key`, mas mudar o valor de R$ 25,00 para R$ 50,00, o sistema detecta a fraude e devolve `409 Conflict`.

### Nível 2: Double-Check sob o Lock `FOR UPDATE`
Para 50 requisições que chegam exatamente no mesmo nanossegundo:
1. Todas as 50 chegam antes de haver qualquer registro no banco.
2. A primeira adquire o lock `FOR UPDATE` da carteira. As outras 49 ficam na fila do PostgreSQL aguardando.
3. A primeira comita o débito e insere a transação.
4. A segunda é liberada da fila. **Aqui está o segredo:** assim que ela adquire o lock, ela faz uma nova busca no banco (`GetTransactionByIdempotencyKeyTx`).
5. Ela descobre que a transação já foi processada pelo worker anterior! Então dá rollback e retorna o replay imediatamente sem debitar a carteira novamente!

### Nível 3: Constraints Únicas no PostgreSQL
```sql
CONSTRAINT uq_transactions_provider_external UNIQUE (provider_id, external_transaction_id),
CONSTRAINT uq_transactions_idempotency UNIQUE (idempotency_key)
```
Se qualquer tentativa escapar, o PostgreSQL recusa o insert com código de erro `23505 (unique_violation)`.

---

## 7. O Padrão Transactional Outbox

### O Problema do "Dual-Write":
Imagina o seguinte cenário:
1. Tua aplicação comita o débito da carteira no PostgreSQL.
2. Em seguida, tenta publicar o evento no SQS/Kafka.
3. A rede cai bem nesse milissegundo!
O dinheiro foi tirado do jogador, mas o evento nunca chegou aos outros sistemas. O sistema entrou em estado inconsistente.

### A Solução Outbox:
Não chamamos a rede externa dentro da transação financeira.
1. Na mesma transação SQL que atualiza a carteira, inserimos uma linha na tabela `outbox_events` com status `PENDING`.
2. Como ambas estão dentro do mesmo `COMMIT`, **ou ambas gravam, ou nada grava**.
3. Um worker separado (`OutboxPublisherWorker`) faz a leitura:
   ```sql
   SELECT ... FROM outbox_events WHERE status = 'PENDING' FOR UPDATE SKIP LOCKED
   ```
4. O `SKIP LOCKED` permite que múltiplos publicadores em paralelo leiam registros diferentes sem travar uns aos outros.
5. Após publicar com sucesso no barramento, o registro é marcado como `PUBLISHED`.

---

## 8. Mensageria SQS FIFO e Inbox Pattern

### Filas FIFO:
- `wager-transactions.fifo`: Fila principal com garantia de entrega ordenada.
- `MessageGroupId`: Definido como `"wallet-" + walletId`. Garante que eventos da mesma carteira sejam processados rigorosamente em ordem, enquanto carteiras diferentes rodam em paralelo.
- `wager-transactions-dlq.fifo`: Fila morta (Dead Letter Queue). Se uma mensagem falhar 3 vezes seguidas por erro inesperado, ela é movida automaticamente para a DLQ para não travar a fila.

### O Inbox Pattern:
Para evitar que uma mensagem reentregue pelo SQS execute um débito repetido:
- A tabela `inbox_messages` guarda o `message_id` da mensagem processada.
- Antes de processar, o worker verifica se o `message_id` já existe na tabela. Se existir, ele apenas descarta a mensagem da fila com segurança.

### Resolução Fora de Ordem (`PendingReferenceWorker`):
Se um estorno (`REFUND`) chegar antes da aposta (`BET`):
1. O `REFUND` é salvo no banco como `PENDING_REFERENCE`.
2. O saldo do jogador não muda.
3. Quando a aposta original finalmente chega e é processada, o `PendingReferenceWorker` encontra a aposta, resolve o estorno, credita o saldo da carteira e transiciona o status para `PROCESSED`.

---

## 9. Uber Fx: Injeção de Dependências

O Uber Fx (`go.uber.org/fx`) organiza a aplicação de forma declarativa e sem variáveis globais.

1. **`fx.Provide`**: Registra construtores de dependências (Config, Banco, Repositório, Serviços, Handlers).
2. **`fx.Invoke`**: Aciona o ciclo de vida da aplicação (`fx.Lifecycle`).
3. **Graceful Shutdown**:
   - Quando a aplicação recebe um sinal de encerramento (`SIGTERM` ou `Ctrl+C`):
   - Primeiro cancela o contexto dos workers assíncronos (`cancelWorkers()`).
   - Dá um shutdown limpo no servidor HTTP (`server.Shutdown`), esperando as requisições em andamento terminarem.
   - Por último, fecha o pool de conexões do PostgreSQL (`pool.Close()`).
   - Nenhuma transação fica aberta pela metade.

---

## 10. Segurança OIDC e Isolamento de Provedores

- Integrado nativamente ao **Keycloak** via OAuth 2.0 / OpenID Connect.
- A aplicação baixa periodicamente as chaves públicas criptográficas do endpoint JWKS (`/certs`) e valida tokens JWT assinados com RS256.
- **Isolamento de Provedor (*Multi-tenancy*):**
  - O token do provedor contém a claim `providerId: "provider-a"`.
  - Se o `provider-a` tentar consultar ou operar dados do `provider-b`, o middleware HTTP intercepta e retorna imediatamente:
    ```json
    HTTP 403 Forbidden: "forbidden: provider token does not match requested providerId"
    ```

---

## 11. Roteiro de Defesa na Entrevista

Aqui estão as perguntas mais prováveis que um Tech Lead ou Arquiteto Sênior fará na tua entrevista técnica, e exatamente como tu deves responder:

### Pergunta 1: "Por que você escolheu `int64` para dinheiro em vez de `float64`?"
**Resposta:**
*"Float64 em Go segue o padrão binário IEEE 754, que não consegue representar frações decimais finitas com precisão exata, gerando dízimas como 0.30000000000000004. Em um sistema de apostas, esses desvios causariam inconsistência contábil e auditoria reprovada. Por isso, criei um Value Object Money imutável que guarda o valor estritamente em centavos inteiros (int64) com moeda ISO 4217, garantindo precisão matemática absoluta e prevenção de overflow."*

---

### Pergunta 2: "Como você lidou com concorrência quando duas apostas de R$ 80 chegam ao mesmo tempo para um saldo de R$ 100?"
**Resposta:**
*"Coordenei a concorrência a nível de carteira individual usando bloqueio pessimista no PostgreSQL com `SELECT ... FOR UPDATE`. Quando as duas requisições chegam em paralelo, o Postgres enfileira a segunda sob o lock da linha daquela carteira. A primeira debita R$ 80, o saldo vai para R$ 20 e comita. A segunda é liberada, lê o saldo atualizado de R$ 20, identifica saldo insuficiente e rejeita a transação com o código estável `INSUFFICIENT_FUNDS`, sem gerar débito no ledger e mantendo o saldo final em R$ 20,00. Carteiras de outros jogadores progridem em paralelo sem contenção."*

---

### Pergunta 3: "O que acontece se a rede falhar e o mesmo request de aposta for enviado 50 vezes simultaneamente?"
**Resposta:**
*"Implementei idempotência em três níveis. Primeiro, calculo um hash SHA-256 canônico a partir dos dados de negócio. Segundo, ao adquirir o lock `FOR UPDATE` da carteira, faço um double-check da transação na mesma sessão SQL. Quando a primeira requisição comita, as outras 49 identificam a transação já concluída e devolvem o replay idêntico com `idempotentReplay: true`. Como terceira camada de defesa, tenho constraints de unicidade no banco (`uq_transactions_idempotency`). Cobri exatamente esse cenário no teste automatizado `TestIntegration_Concurrency_50IdenticalBets`."*

---

### Pergunta 4: "O que é o Transactional Outbox e por que você usou?"
**Resposta:**
*"Utilizei o Transactional Outbox para resolver o problema de dual-write entre o banco relacional e o mensageiro. Em vez de comitar no banco e tentar chamar o SQS diretamente — correndo o risco da rede cair entre as duas etapas —, eu gravo o evento na tabela `outbox_events` na mesma transação atômica que atualiza a carteira e o ledger. Um worker assíncrono lê os eventos pendentes usando `SELECT ... FOR UPDATE SKIP LOCKED`, permitindo que múltiplos publicadores escalem horizontalmente sem colisão."*

---

### Pergunta 5: "E se um `REFUND` chegar antes da `BET` original?"
**Resposta:**
*"Em mensageria distribuída, a entrega fora de ordem é esperada. Quando o `REFUND` chega sem a aposta existir, eu não falho com erro 500 nem descarto a mensagem; persisto a operação com status `PENDING_REFERENCE` e emito evento no outbox. O `PendingReferenceWorker` monitora essas pendências. Assim que a aposta original é processada, o worker resolve a referência, credita o saldo na carteira e comita como `PROCESSED`. Se o tempo limite (TTL) de 1 minuto expirar sem a referência chegar, ela é finalizada definitivamente como `REJECTED`."*

---

### Pergunta 6: "Como você garante que o saldo de uma carteira nunca foi adulterado?"
**Resposta:**
*"Através de um livro-razão (ledger) estritamente imutável e append-only. Criei um trigger em PL/pgSQL que bloqueia qualquer comando `UPDATE` ou `DELETE` na tabela `wallet_ledger_entries`. Além disso, disponibilizei o endpoint `POST /wallets/{walletId}/reconciliation`, que soma todos os créditos e subtrai todos os débitos do histórico do ledger e compara com o saldo armazenado na carteira, comprovando diferença zero."*

---

## 🏆 Conclusão

Tu tens nas mãos um projeto de nível de **Engenheiro de Software Sênior**:
- Concorrência de banco testada com race detector (`-race`).
- 100% dos testes unitários e de integração com containers reais passando.
- Código limpo, desacoplado, auditável e documentado.

Estuda este manual, respira fundo e vai pra cima com calma. Tu entendeste a arquitetura e o código é teu. Égua mano, vai dar tudo certo! 🚀
