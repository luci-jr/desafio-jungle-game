#!/usr/bin/env bash
# ==============================================================================
# Script de Testes E2E Automatizados — Desafio Jungle Game (Backend Go)
# ==============================================================================
# Executa a bateria completa de 21 cenários de teste contra a API em execução,
# validando o fluxo principal (Happy Path) e a matriz de resiliência e erros.
# ==============================================================================

# Validação de dependências mínimas do host
for cmd in curl jq docker; do
  if ! command -v $cmd >/dev/null 2>&1; then
    echo "FALHA: O comando '$cmd' não está instalado no sistema host."
    echo "Instale '$cmd' para executar esta suíte de testes E2E."
    exit 1
  fi
done

BASE_URL="http://localhost:8000"
KEYCLOAK_URL="http://localhost:8080"
LOCALSTACK_URL="http://localhost:4566"
RUN_ID="$(date +%s)"
PLAYER_ID="player-$RUN_ID"

echo "================================================================="
echo "   BATERIA COMPLETA DE TESTES: DESAFIO JUNGLE GAME (BACKEND GO)  "
echo "================================================================="

# 1. Healthcheck
echo -n "[TEST 01] Healthcheck (/health/ready)... "
HEALTH=$(curl -s $BASE_URL/health/ready)
DB_STATUS=$(echo "$HEALTH" | jq -r .database)
SQS_STATUS=$(echo "$HEALTH" | jq -r .sqs)
APP_STATUS=$(echo "$HEALTH" | jq -r .status)
if [ "$DB_STATUS" = "CONNECTED" ] && [ "$SQS_STATUS" = "CONNECTED" ] && [ "$APP_STATUS" = "UP" ]; then
  echo "OK (DB: $DB_STATUS, SQS: $SQS_STATUS, App: $APP_STATUS)"
else
  echo "FALHOU: $HEALTH"
  exit 1
fi

# 2. Obter Tokens Keycloak
echo -n "[TEST 02] Autenticação Keycloak (Tokens JWT)... "
TOKEN_INTERNAL=$(curl -s -H "Host: keycloak:8080" -X POST $KEYCLOAK_URL/realms/betting/protocol/openid-connect/token \
  -d "grant_type=client_credentials" \
  -d "client_id=internal-service" \
  -d "client_secret=secret-internal" | jq -r .access_token)

TOKEN_PROV_A=$(curl -s -H "Host: keycloak:8080" -X POST $KEYCLOAK_URL/realms/betting/protocol/openid-connect/token \
  -d "grant_type=client_credentials" \
  -d "client_id=provider-a" \
  -d "client_secret=secret-a" | jq -r .access_token)

TOKEN_PROV_B=$(curl -s -H "Host: keycloak:8080" -X POST $KEYCLOAK_URL/realms/betting/protocol/openid-connect/token \
  -d "grant_type=client_credentials" \
  -d "client_id=provider-b" \
  -d "client_secret=secret-b" | jq -r .access_token)

if [ -n "$TOKEN_INTERNAL" ] && [ -n "$TOKEN_PROV_A" ] && [ -n "$TOKEN_PROV_B" ] && [ "$TOKEN_INTERNAL" != "null" ]; then
  echo "OK (Tokens gerados com sucesso)"
else
  echo "FALHOU ao gerar tokens"
  exit 1
fi

# 3. Criar Carteira
echo -n "[TEST 03] Criação de Carteira (Saldo R$ 500.00)... "
WALLET_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wallets \
  -H "Authorization: Bearer $TOKEN_INTERNAL" \
  -H "Content-Type: application/json" \
  -d "{
    \"playerId\": \"$PLAYER_ID\",
    \"initialBalance\": {
      \"amount\": \"500.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$WALLET_RESP" | tail -n1)
WALLET_BODY=$(echo "$WALLET_RESP" | sed '$d')
WALLET_ID=$(echo "$WALLET_BODY" | jq -r .id)
BALANCE_AMOUNT=$(echo "$WALLET_BODY" | jq -r .balance.amount)

if [ "$HTTP_CODE" = "201" ] && [ "$BALANCE_AMOUNT" = "500.00" ]; then
  echo "OK (Wallet ID: $WALLET_ID, Saldo: R$ $BALANCE_AMOUNT)"
else
  echo "FALHOU: HTTP $HTTP_CODE - $WALLET_BODY"
  exit 1
fi

# 4. Consulta de Saldo e Ledger
echo -n "[TEST 04] Consulta de Saldo e Livro-Razão (Ledger)... "
LEDGER_RESP=$(curl -s -w "\n%{http_code}" -X GET "$BASE_URL/wallets/$WALLET_ID/ledger?limit=10" \
  -H "Authorization: Bearer $TOKEN_INTERNAL")
HTTP_CODE=$(echo "$LEDGER_RESP" | tail -n1)
LEDGER_BODY=$(echo "$LEDGER_RESP" | sed '$d')
ITEMS_COUNT=$(echo "$LEDGER_BODY" | jq '.entries | length')
if [ "$HTTP_CODE" = "200" ] && [ "$ITEMS_COUNT" -ge 1 ]; then
  echo "OK (Lançamentos encontrados: $ITEMS_COUNT)"
else
  echo "FALHOU: HTTP $HTTP_CODE - $LEDGER_BODY"
  exit 1
fi

# 5. Débito de Aposta (BET) de R$ 50.00
TX_BET_ID="tx-bet-$RUN_ID"
echo -n "[TEST 05] Débito de Aposta BET (R$ 50.00)... "
BET_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_BET_ID" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_BET_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"BET\",
    \"money\": {
      \"amount\": \"50.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$BET_RESP" | tail -n1)
BET_BODY=$(echo "$BET_RESP" | sed '$d')
BET_STATUS=$(echo "$BET_BODY" | jq -r .status)
NEW_BALANCE=$(echo "$BET_BODY" | jq -r .balance.amount)

if [ "$HTTP_CODE" = "200" ] && [ "$BET_STATUS" = "PROCESSED" ] && [ "$NEW_BALANCE" = "450.00" ]; then
  echo "OK (Status: $BET_STATUS, Novo Saldo: R$ $NEW_BALANCE)"
else
  echo "FALHOU: HTTP $HTTP_CODE - $BET_BODY"
  exit 1
fi

# 6. Idempotência (Replay da mesma aposta)
echo -n "[TEST 06] Idempotência (Replay idêntico da aposta)... "
REPLAY_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_BET_ID" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_BET_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"BET\",
    \"money\": {
      \"amount\": \"50.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$REPLAY_RESP" | tail -n1)
REPLAY_BODY=$(echo "$REPLAY_RESP" | sed '$d')
IS_REPLAY=$(echo "$REPLAY_BODY" | jq -r .idempotentReplay)
REPLAY_BALANCE=$(echo "$REPLAY_BODY" | jq -r .balance.amount)

if [ "$HTTP_CODE" = "200" ] && [ "$IS_REPLAY" = "true" ] && [ "$REPLAY_BALANCE" = "450.00" ]; then
  echo "OK (IdempotentReplay: true, Saldo mantido: R$ $REPLAY_BALANCE)"
else
  echo "FALHOU: HTTP $HTTP_CODE - $REPLAY_BODY"
  exit 1
fi

# 7. Ganho (WIN) de R$ 120.00
TX_WIN_ID="tx-win-$RUN_ID"
echo -n "[TEST 07] Ganho WIN (R$ 120.00)... "
WIN_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_WIN_ID" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_WIN_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"WIN\",
    \"money\": {
      \"amount\": \"120.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$WIN_RESP" | tail -n1)
WIN_BODY=$(echo "$WIN_RESP" | sed '$d')
WIN_STATUS=$(echo "$WIN_BODY" | jq -r .status)
WIN_BALANCE=$(echo "$WIN_BODY" | jq -r .balance.amount)

if [ "$HTTP_CODE" = "200" ] && [ "$WIN_STATUS" = "PROCESSED" ] && [ "$WIN_BALANCE" = "570.00" ]; then
  echo "OK (Status: $WIN_STATUS, Novo Saldo: R$ $WIN_BALANCE)"
else
  echo "FALHOU: HTTP $HTTP_CODE - $WIN_BODY"
  exit 1
fi

# 8. Estorno (REFUND) de R$ 50.00
TX_REFUND_ID="tx-refund-$RUN_ID"
echo -n "[TEST 08] Estorno REFUND referenciado (R$ 50.00)... "
REFUND_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_REFUND_ID" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_REFUND_ID\",
    \"referenceExternalTransactionId\": \"$TX_BET_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"REFUND\",
    \"money\": {
      \"amount\": \"50.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$REFUND_RESP" | tail -n1)
REFUND_BODY=$(echo "$REFUND_RESP" | sed '$d')
REFUND_STATUS=$(echo "$REFUND_BODY" | jq -r .status)
REFUND_BALANCE=$(echo "$REFUND_BODY" | jq -r .balance.amount)

if [ "$HTTP_CODE" = "200" ] && [ "$REFUND_STATUS" = "PROCESSED" ] && [ "$REFUND_BALANCE" = "620.00" ]; then
  echo "OK (Status: $REFUND_STATUS, Novo Saldo: R$ $REFUND_BALANCE)"
else
  echo "FALHOU: HTTP $HTTP_CODE - $REFUND_BODY"
  exit 1
fi

# 9. Reconciliação Contábil da Carteira
echo -n "[TEST 09] Auditoria de Reconciliação Contábil... "
REC_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wallets/$WALLET_ID/reconciliation \
  -H "Authorization: Bearer $TOKEN_INTERNAL")
HTTP_CODE=$(echo "$REC_RESP" | tail -n1)
REC_BODY=$(echo "$REC_RESP" | sed '$d')
IS_CONSISTENT=$(echo "$REC_BODY" | jq -r .consistent)
DIFF=$(echo "$REC_BODY" | jq -r .difference.amount)
ENTRIES=$(echo "$REC_BODY" | jq -r .checkedEntries)

if [ "$HTTP_CODE" = "200" ] && [ "$IS_CONSISTENT" = "true" ] && [ "$DIFF" = "0.00" ]; then
  echo "OK (Consistente: $IS_CONSISTENT, Lançamentos: $ENTRIES, Diferença: R$ $DIFF)"
else
  echo "FALHOU: HTTP $HTTP_CODE - $REC_BODY"
  exit 1
fi

# 10. Mensageria Assíncrona SQS FIFO
echo -n "[TEST 10] Mensageria Assíncrona SQS FIFO (LocalStack)... "
MSG_ID="sqs-$RUN_ID"
EXT_TX_SQS="ext-sqs-$RUN_ID"
SQS_PAYLOAD="{
    \"messageId\": \"$MSG_ID\",
    \"type\": \"WagerTransactionRequested\",
    \"occurredAt\": \"2026-09-15T21:00:00Z\",
    \"data\": {
      \"providerId\": \"provider-a\",
      \"externalTransactionId\": \"$EXT_TX_SQS\",
      \"idempotencyKey\": \"provider-a:$EXT_TX_SQS\",
      \"playerId\": \"$PLAYER_ID\",
      \"walletId\": \"$WALLET_ID\",
      \"roundId\": \"round-sqs-$RUN_ID\",
      \"gameId\": \"game-jungle-slots\",
      \"kind\": \"BET\",
      \"money\": {\"amount\": \"20.00\", \"currency\": \"BRL\"}
    }
}"

if command -v aws >/dev/null 2>&1; then
  AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test aws --region us-east-1 --endpoint-url=http://localhost:4566 sqs send-message \
    --queue-url http://localhost:4566/000000000000/wager-transactions.fifo \
    --message-group-id "wallet-$WALLET_ID" \
    --message-deduplication-id "$MSG_ID" \
    --message-body "$SQS_PAYLOAD" > /dev/null
else
  docker exec betting-localstack awslocal sqs send-message \
    --queue-url http://localhost:4566/000000000000/wager-transactions.fifo \
    --message-group-id "wallet-$WALLET_ID" \
    --message-deduplication-id "$MSG_ID" \
    --message-body "$SQS_PAYLOAD" > /dev/null
fi

sleep 1.5

BAL_SQS=$(curl -s -X GET $BASE_URL/wallets/$WALLET_ID -H "Authorization: Bearer $TOKEN_INTERNAL" | jq -r .balance.amount)
if [ "$BAL_SQS" = "600.00" ]; then
  echo "OK (Saldo debitado pelo Worker: R$ $BAL_SQS)"
else
  echo "FALHOU: Esperava R$ 600.00, recebeu R$ $BAL_SQS"
  exit 1
fi

# 11. Cenário E1: Carteira Duplicada (409 Conflict)
echo -n "[TEST 11] Cenário E1: Carteira Duplicada (Esperado 409 Conflict)... "
DUP_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wallets \
  -H "Authorization: Bearer $TOKEN_INTERNAL" \
  -H "Content-Type: application/json" \
  -d "{
    \"playerId\": \"$PLAYER_ID\",
    \"initialBalance\": {
      \"amount\": \"100.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$DUP_RESP" | tail -n1)
if [ "$HTTP_CODE" = "409" ]; then
  echo "OK (Retornou HTTP 409 Conflict corretamente)"
else
  echo "FALHOU: Esperava 409, recebeu $HTTP_CODE"
  exit 1
fi

# 12. Cenário E2: Saldo Insuficiente (Aposta de R$ 99999.00 -> REJECTED / INSUFFICIENT_FUNDS)
echo -n "[TEST 12] Cenário E2: Saldo Insuficiente (Esperado REJECTED / INSUFFICIENT_FUNDS)... "
TX_INSUF_ID="tx-insuf-$RUN_ID"
INSUF_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_INSUF_ID" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_INSUF_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-insuf-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"BET\",
    \"money\": {
      \"amount\": \"99999.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$INSUF_RESP" | tail -n1)
INSUF_BODY=$(echo "$INSUF_RESP" | sed '$d')
STATUS_INSUF=$(echo "$INSUF_BODY" | jq -r .status)
FAIL_CODE=$(echo "$INSUF_BODY" | jq -r .failureCode)
if [ "$HTTP_CODE" = "200" ] && [ "$STATUS_INSUF" = "REJECTED" ] && [ "$FAIL_CODE" = "INSUFFICIENT_FUNDS" ]; then
  echo "OK (Status: $STATUS_INSUF, FailureCode: $FAIL_CODE)"
else
  echo "FALHOU: HTTP $HTTP_CODE - $INSUF_BODY"
  exit 1
fi

# 13. Cenário E3: Moeda Divergente (USD)
echo -n "[TEST 13] Cenário E3: Moeda Divergente USD (Esperado 422)... "
TX_CURR_ID="tx-curr-$RUN_ID"
CURR_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_CURR_ID" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_CURR_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-curr-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"BET\",
    \"money\": {
      \"amount\": \"10.00\",
      \"currency\": \"USD\"
    }
  }")
HTTP_CODE=$(echo "$CURR_RESP" | tail -n1)
if [ "$HTTP_CODE" = "422" ]; then
  echo "OK (Retornou HTTP 422 Unprocessable Entity)"
else
  echo "FALHOU: Esperava 422, recebeu $HTTP_CODE"
  exit 1
fi

# 14. Cenário E4: Conflito de Idempotência (Mesma Chave, Payload Diferente)
echo -n "[TEST 14] Cenário E4: Conflito de Idempotência (Esperado 409)... "
CONF_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_BET_ID" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_BET_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"BET\",
    \"money\": {
      \"amount\": \"99.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$CONF_RESP" | tail -n1)
if [ "$HTTP_CODE" = "409" ]; then
  echo "OK (Retornou HTTP 409 Conflict)"
else
  echo "FALHOU: Esperava 409, recebeu $HTTP_CODE"
  exit 1
fi

# 15. Cenário E5: Header Idempotency-Key Ausente
echo -n "[TEST 15] Cenário E5: Header Idempotency Ausente (Esperado 400)... "
HEADER_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"tx-no-head-$RUN_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-head-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"BET\",
    \"money\": {
      \"amount\": \"10.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$HEADER_RESP" | tail -n1)
if [ "$HTTP_CODE" = "400" ]; then
  echo "OK (Retornou HTTP 400 Bad Request)"
else
  echo "FALHOU: Esperava 400, recebeu $HTTP_CODE"
  exit 1
fi

# 16. Cenário E6: Falha de Autenticação (Token Inválido)
echo -n "[TEST 16] Cenário E6: Falha de Autenticação 401 (Esperado 401)... "
AUTH_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer token-falso" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: prov:fake" \
  -d '{"kind":"BET"}')
HTTP_CODE=$(echo "$AUTH_RESP" | tail -n1)
if [ "$HTTP_CODE" = "401" ]; then
  echo "OK (Retornou HTTP 401 Unauthorized)"
else
  echo "FALHOU: Esperava 401, recebeu $HTTP_CODE"
  exit 1
fi

# 17. Cenário E7: Violação de Tenancy (Token B operando como Provider A)
echo -n "[TEST 17] Cenário E7: Violação de Tenancy (Esperado 403)... "
TX_TENANT_ID="tx-tenant-$RUN_ID"
TENANT_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_B" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_TENANT_ID" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_TENANT_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-ten-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"BET\",
    \"money\": {
      \"amount\": \"10.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$TENANT_RESP" | tail -n1)
if [ "$HTTP_CODE" = "403" ]; then
  echo "OK (Retornou HTTP 403 Forbidden)"
else
  echo "FALHOU: Esperava 403, recebeu $HTTP_CODE"
  exit 1
fi

# 18. Cenário E8-A: Estorno de Transação que ainda não existe (Out-of-Order -> PENDING_REFERENCE)
echo -n "[TEST 18] Cenário E8-A: Estorno Fora de Ordem (Esperado PENDING_REFERENCE)... "
TX_REF_NF="tx-ref-nf-$RUN_ID"
REF_PENDING_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_REF_NF" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_REF_NF\",
    \"referenceExternalTransactionId\": \"tx-nunca-existiu-$RUN_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-ref-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"REFUND\",
    \"money\": {
      \"amount\": \"10.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$REF_PENDING_RESP" | tail -n1)
STATUS_PENDING=$(echo "$REF_PENDING_RESP" | sed '$d' | jq -r .status)
if [ "$HTTP_CODE" = "200" ] && [ "$STATUS_PENDING" = "PENDING_REFERENCE" ]; then
  echo "OK (Status: PENDING_REFERENCE retornado para resolução assíncrona)"
else
  echo "FALHOU: HTTP $HTTP_CODE - $REF_PENDING_RESP"
  exit 1
fi

# 19. Cenário E8-B: Segundo Estorno da Mesma Aposta (Já Estornada -> 422 Unprocessable Entity)
echo -n "[TEST 19] Cenário E8-B: Estorno Duplicado da mesma BET (Esperado 422)... "
TX_REF_DUP="tx-ref-dup-$RUN_ID"
REF_DUP_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_REF_DUP" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_REF_DUP\",
    \"referenceExternalTransactionId\": \"$TX_BET_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"REFUND\",
    \"money\": {
      \"amount\": \"50.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$REF_DUP_RESP" | tail -n1)
if [ "$HTTP_CODE" = "422" ]; then
  echo "OK (Retornou HTTP 422 Unprocessable Entity)"
else
  echo "FALHOU: Esperava 422, recebeu $HTTP_CODE"
  exit 1
fi

# 20. Cenário E9: Imutabilidade do Ledger (Trigger SQL bloqueia UPDATE e DELETE)
echo -n "[TEST 20] Cenário E9: Imutabilidade do Ledger via Trigger SQL... "
SQL_ERR=$(docker exec betting-postgres psql -U postgres -d betting_db -c \
  "UPDATE wallet_ledger_entries SET amount = 999999 WHERE wallet_id = '$WALLET_ID';" 2>&1 || true)
if echo "$SQL_ERR" | grep -qi "imutaveis"; then
  echo "OK (PostgreSQL bloqueou UPDATE com trigger de imutabilidade)"
else
  echo "FALHOU: $SQL_ERR"
  exit 1
fi

# 21. Cenário E10: Valor Zero ou Negativo (Esperado 422)
echo -n "[TEST 21] Cenário E10: Valor Zero Rejeitado (Esperado 422)... "
TX_ZERO_ID="tx-zero-$RUN_ID"
ZERO_RESP=$(curl -s -w "\n%{http_code}" -X POST $BASE_URL/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROV_A" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: provider-a:$TX_ZERO_ID" \
  -d "{
    \"providerId\": \"provider-a\",
    \"externalTransactionId\": \"$TX_ZERO_ID\",
    \"playerId\": \"$PLAYER_ID\",
    \"walletId\": \"$WALLET_ID\",
    \"roundId\": \"round-zero-$RUN_ID\",
    \"gameId\": \"jungle-slots\",
    \"kind\": \"BET\",
    \"money\": {
      \"amount\": \"0.00\",
      \"currency\": \"BRL\"
    }
  }")
HTTP_CODE=$(echo "$ZERO_RESP" | tail -n1)
if [ "$HTTP_CODE" = "422" ]; then
  echo "OK (Retornou HTTP 422 Unprocessable Entity)"
else
  echo "FALHOU: Esperava 422, recebeu $HTTP_CODE"
  exit 1
fi

echo "================================================================="
echo "  SUCESSO TOTAL! TODOS OS 21 CENÁRIOS E2E FORAM APROVADOS!       "
echo "================================================================="
