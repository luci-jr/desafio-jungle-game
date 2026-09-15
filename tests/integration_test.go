package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"backend-challenge-go/internal/application"
	"backend-challenge-go/internal/domain"
	"backend-challenge-go/internal/infrastructure/auth"
	"backend-challenge-go/internal/infrastructure/database"
	internalHttp "backend-challenge-go/internal/infrastructure/http"
	"backend-challenge-go/internal/infrastructure/messaging"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestApp(t *testing.T) (*httptest.Server, *database.Repository, *pgxpool.Pool, string) {
	pool, err := database.NewPostgresPool(database.Config{
		Host:     "localhost",
		Port:     "5432",
		User:     "postgres",
		Password: "postgres",
		Database: "betting_db",
		SSLMode:  "disable",
		MaxConns: 30,
		MinConns: 5,
	})
	require.NoError(t, err, "falha ao conectar no PostgreSQL")

	// Os testes são executados no pacote tests e não passam pelo ciclo de vida
	// do Fx. Por isso, a migration precisa ser aplicada explicitamente aqui.
	if err := database.RunMigrations(pool, testMigrationPath(t)); err != nil {
		pool.Close()
		require.NoError(t, err, "falha ao aplicar as migrations de teste")
	}

	repo := database.NewRepository(pool)
	walletService := application.NewWalletService(repo)
	wagerService := application.NewWagerService(repo)
	reconcileService := application.NewReconciliationService(repo)

	jwksURL := "http://localhost:8080/realms/betting/protocol/openid-connect/certs"
	issuer := "http://localhost:8080/realms/betting"
	tokenValidator := auth.NewTokenValidator(jwksURL, issuer)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := internalHttp.NewHandler(walletService, wagerService, reconcileService, pool)
	router := internalHttp.NewRouter(handler, tokenValidator, logger)

	server := httptest.NewServer(router)

	// Obtém token real de teste do Keycloak para provider-a
	tokenA := getOAuthToken(t, "provider-a", "secret-a")

	return server, repo, pool, tokenA
}

// testMigrationPath localiza a migration a partir do próprio arquivo de teste.
// Isso evita depender do diretório de trabalho usado pelo go test.
func testMigrationPath(t *testing.T) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "não foi possível localizar o arquivo do teste")

	return filepath.Join(filepath.Dir(currentFile), "..", "migrations", "000001_init_schema.up.sql")
}

func getOAuthToken(t *testing.T, clientID, clientSecret string) string {
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)

	resp, err := http.Post(
		"http://localhost:8080/realms/betting/protocol/openid-connect/token",
		"application/x-www-form-urlencoded",
		bytes.NewBufferString(data.Encode()),
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	require.NotEmpty(t, result.AccessToken)

	return result.AccessToken
}

// 1. Teste de Imutabilidade do Ledger (Trigger no Banco)
func TestIntegration_LedgerImmutability(t *testing.T) {
	server, _, pool, token := setupTestApp(t)
	defer server.Close()
	defer pool.Close()

	// Cria carteira com saldo para gerar 1 lançamento no ledger
	playerID := uuid.NewString()
	createReq := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"50.00","currency":"BRL"}}`, playerID)
	req, _ := http.NewRequest("POST", server.URL+"/wallets", bytes.NewBufferString(createReq))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	var walletResp struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&walletResp)

	ctx := context.Background()
	// Tentar atualizar o registro do ledger deve estourar erro do trigger
	_, err = pool.Exec(ctx, "UPDATE wallet_ledger_entries SET amount = 99999 WHERE wallet_id = $1", walletResp.ID)
	assert.Error(t, err, "trigger deve bloquear UPDATE no ledger")
	assert.Contains(t, err.Error(), "imutaveis")

	_, err = pool.Exec(ctx, "DELETE FROM wallet_ledger_entries WHERE wallet_id = $1", walletResp.ID)
	assert.Error(t, err, "trigger deve bloquear DELETE no ledger")
	assert.Contains(t, err.Error(), "imutaveis")
}

// 2. Teste de Abertura de Carteira, Ledger e Reconciliação
func TestIntegration_WalletCreationAndReconciliation(t *testing.T) {
	server, _, pool, token := setupTestApp(t)
	defer server.Close()
	defer pool.Close()

	playerID := uuid.NewString()

	// 1. Criar carteira com 1000.00 BRL
	reqBody := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"1000.00","currency":"BRL"}}`, playerID)
	req, _ := http.NewRequest("POST", server.URL+"/wallets", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	assert.Equal(t, http.StatusCreated, res.StatusCode)

	var walletResp struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
		Balance struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"balance"`
	}
	err = json.NewDecoder(res.Body).Decode(&walletResp)
	require.NoError(t, err)
	assert.Equal(t, "1000.00", walletResp.Balance.Amount)
	assert.Equal(t, int64(1), walletResp.Version)

	walletID := walletResp.ID

	// 2. Reconciliação imediata: deve ser consistente com 1 lançamento (OPENING)
	reqRec, _ := http.NewRequest("POST", server.URL+"/wallets/"+walletID+"/reconciliation", nil)
	reqRec.Header.Set("Authorization", "Bearer "+token)

	resRec, err := http.DefaultClient.Do(reqRec)
	require.NoError(t, err)
	defer resRec.Body.Close()

	assert.Equal(t, http.StatusOK, resRec.StatusCode)

	var recResp struct {
		WalletID          string                  `json:"walletId"`
		Consistent        bool                    `json:"consistent"`
		CheckedEntries    int64                   `json:"checkedEntries"`
		StoredBalance     struct{ Amount string } `json:"storedBalance"`
		CalculatedBalance struct{ Amount string } `json:"calculatedBalance"`
		Difference        struct{ Amount string } `json:"difference"`
	}
	err = json.NewDecoder(resRec.Body).Decode(&recResp)
	require.NoError(t, err)
	assert.True(t, recResp.Consistent)
	assert.Equal(t, int64(1), recResp.CheckedEntries)
	assert.Equal(t, "1000.00", recResp.StoredBalance.Amount)
	assert.Equal(t, "1000.00", recResp.CalculatedBalance.Amount)
	assert.Equal(t, "0.00", recResp.Difference.Amount)
}

// 3. Teste de Concorrência 1: 50 Requisições Simultâneas da Mesma Aposta (Deduplicação / Idempotência)
func TestIntegration_Concurrency_50IdenticalBets(t *testing.T) {
	server, _, pool, token := setupTestApp(t)
	defer server.Close()
	defer pool.Close()

	playerID := uuid.NewString()

	// 1. Criar carteira com 100.00 BRL
	createReq := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"100.00","currency":"BRL"}}`, playerID)
	req, _ := http.NewRequest("POST", server.URL+"/wallets", bytes.NewBufferString(createReq))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	var walletResp struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&walletResp)
	walletID := walletResp.ID

	// 2. Disparar 50 requisições simultâneas com a MESMA chave de idempotência e mesmo payload
	externalTxID := "ext-" + uuid.NewString()
	idempotencyKey := "provider-a:" + externalTxID

	payload := fmt.Sprintf(`{
		"providerId": "provider-a",
		"externalTransactionId": "%s",
		"playerId": "%s",
		"walletId": "%s",
		"roundId": "round-concurrency-50",
		"gameId": "game-slots",
		"kind": "BET",
		"money": {"amount": "25.00", "currency": "BRL"}
	}`, externalTxID, playerID, walletID)

	const totalWorkers = 50
	var wg sync.WaitGroup
	results := make(chan int, totalWorkers)
	replayFlags := make(chan bool, totalWorkers)

	for i := 0; i < totalWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := http.NewRequest("POST", server.URL+"/wagering/transactions", bytes.NewBufferString(payload))
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", idempotencyKey)

			resp, err := http.DefaultClient.Do(r)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			results <- resp.StatusCode
			var respData struct {
				IdempotentReplay bool `json:"idempotentReplay"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&respData)
			replayFlags <- respData.IdempotentReplay
		}()
	}

	wg.Wait()
	close(results)
	close(replayFlags)

	successCount := 0
	replayCount := 0

	for code := range results {
		if code == http.StatusOK {
			successCount++
		}
	}
	for isReplay := range replayFlags {
		if isReplay {
			replayCount++
		}
	}

	assert.Equal(t, totalWorkers, successCount, "todas as 50 chamadas devem retornar 200 OK")
	assert.Equal(t, totalWorkers-1, replayCount, "exatamente 49 chamadas devem ser identificadas como idempotentReplay")

	// 3. Verifica saldo final: deve ser rigorosamente 75.00 BRL
	reqGet, _ := http.NewRequest("GET", server.URL+"/wallets/"+walletID, nil)
	reqGet.Header.Set("Authorization", "Bearer "+token)
	resGet, err := http.DefaultClient.Do(reqGet)
	require.NoError(t, err)
	defer resGet.Body.Close()

	var finalWallet struct {
		Balance struct{ Amount string } `json:"balance"`
		Version int64                   `json:"version"`
	}
	_ = json.NewDecoder(resGet.Body).Decode(&finalWallet)
	assert.Equal(t, "75.00", finalWallet.Balance.Amount)
	assert.Equal(t, int64(2), finalWallet.Version)

	// 4. Reconciliação deve confirmar exatamente 2 lançamentos (OPENING e 1 BET)
	reqRec, _ := http.NewRequest("POST", server.URL+"/wallets/"+walletID+"/reconciliation", nil)
	reqRec.Header.Set("Authorization", "Bearer "+token)
	resRec, err := http.DefaultClient.Do(reqRec)
	require.NoError(t, err)
	defer resRec.Body.Close()

	var recResp struct {
		Consistent     bool  `json:"consistent"`
		CheckedEntries int64 `json:"checkedEntries"`
	}
	_ = json.NewDecoder(resRec.Body).Decode(&recResp)
	assert.True(t, recResp.Consistent)
	assert.Equal(t, int64(2), recResp.CheckedEntries)
}

// 4. Teste de Disputa de Concorrência (Seção 8 e 13): Duas apostas de 80.00 sobre saldo de 100.00
func TestIntegration_Concurrency_DisputeTwoBets(t *testing.T) {
	server, _, pool, token := setupTestApp(t)
	defer server.Close()
	defer pool.Close()

	playerID := uuid.NewString()

	// 1. Carteira com 100.00 BRL
	createReq := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"100.00","currency":"BRL"}}`, playerID)
	req, _ := http.NewRequest("POST", server.URL+"/wallets", bytes.NewBufferString(createReq))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	var walletResp struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&walletResp)
	walletID := walletResp.ID

	// 2. Duas apostas distintas de 80.00 enviadas ao mesmo tempo
	ext1 := "bet-dispute-1-" + uuid.NewString()
	ext2 := "bet-dispute-2-" + uuid.NewString()

	payload1 := fmt.Sprintf(`{
		"providerId": "provider-a",
		"externalTransactionId": "%s",
		"playerId": "%s",
		"walletId": "%s",
		"roundId": "round-dispute-1",
		"gameId": "game-slots",
		"kind": "BET",
		"money": {"amount": "80.00", "currency": "BRL"}
	}`, ext1, playerID, walletID)

	payload2 := fmt.Sprintf(`{
		"providerId": "provider-a",
		"externalTransactionId": "%s",
		"playerId": "%s",
		"walletId": "%s",
		"roundId": "round-dispute-2",
		"gameId": "game-slots",
		"kind": "BET",
		"money": {"amount": "80.00", "currency": "BRL"}
	}`, ext2, playerID, walletID)

	type responseData struct {
		StatusCode  int
		Status      string `json:"status"`
		FailureCode string `json:"failureCode"`
	}

	resChan := make(chan responseData, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	sendBet := func(extID, payload string) {
		defer wg.Done()
		r, _ := http.NewRequest("POST", server.URL+"/wagering/transactions", bytes.NewBufferString(payload))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "provider-a:"+extID)

		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		bodyBytes, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Logf("DISPUTE ERROR: status=%d, body=%s", resp.StatusCode, string(bodyBytes))
		}
		var data responseData
		data.StatusCode = resp.StatusCode
		_ = json.Unmarshal(bodyBytes, &data)
		resChan <- data
	}

	go sendBet(ext1, payload1)
	go sendBet(ext2, payload2)

	wg.Wait()
	close(resChan)

	processedCount := 0
	rejectedCount := 0

	for r := range resChan {
		assert.Equal(t, http.StatusOK, r.StatusCode)
		switch r.Status {
		case "PROCESSED":
			processedCount++
		case "REJECTED":
			rejectedCount++
			assert.Equal(t, "INSUFFICIENT_FUNDS", r.FailureCode)
		default:
			assert.Failf(t, "status inesperado", "status recebido: %q", r.Status)
		}
	}

	assert.Equal(t, 1, processedCount, "exatamente UMA aposta deve ser processada com sucesso")
	assert.Equal(t, 1, rejectedCount, "exatamente UMA aposta deve ser rejeitada por saldo insuficiente")

	// 3. Saldo final da carteira deve ser exatamente 20.00 BRL
	reqGet, _ := http.NewRequest("GET", server.URL+"/wallets/"+walletID, nil)
	reqGet.Header.Set("Authorization", "Bearer "+token)
	resGet, err := http.DefaultClient.Do(reqGet)
	require.NoError(t, err)
	defer resGet.Body.Close()

	var finalWallet struct {
		Balance struct{ Amount string } `json:"balance"`
		Version int64                   `json:"version"`
	}
	_ = json.NewDecoder(resGet.Body).Decode(&finalWallet)
	assert.Equal(t, "20.00", finalWallet.Balance.Amount)
	assert.Equal(t, int64(2), finalWallet.Version)

	// 4. Reconciliação deve comprovar 2 lançamentos (OPENING e apenas 1 débito de 80.00)
	reqRec, _ := http.NewRequest("POST", server.URL+"/wallets/"+walletID+"/reconciliation", nil)
	reqRec.Header.Set("Authorization", "Bearer "+token)
	resRec, err := http.DefaultClient.Do(reqRec)
	require.NoError(t, err)
	defer resRec.Body.Close()

	var recResp struct {
		Consistent     bool  `json:"consistent"`
		CheckedEntries int64 `json:"checkedEntries"`
	}
	_ = json.NewDecoder(resRec.Body).Decode(&recResp)
	assert.True(t, recResp.Consistent)
	assert.Equal(t, int64(2), recResp.CheckedEntries)
}

// 5. Teste de Autenticação e Isolamento entre Provedores
func TestIntegration_AuthAndProviderIsolation(t *testing.T) {
	server, _, pool, tokenA := setupTestApp(t)
	defer server.Close()
	defer pool.Close()

	tokenB := getOAuthToken(t, "provider-b", "secret-b")

	// 1. Requisição sem token -> 401 Unauthorized
	reqNoAuth, _ := http.NewRequest("GET", server.URL+"/wallets/some-id", nil)
	resNoAuth, err := http.DefaultClient.Do(reqNoAuth)
	require.NoError(t, err)
	defer resNoAuth.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resNoAuth.StatusCode)

	// 2. Provedor A com token válido de Provedor A cria carteira -> 201 Created
	playerID := uuid.NewString()
	createReq := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"10.00","currency":"BRL"}}`, playerID)
	reqA, _ := http.NewRequest("POST", server.URL+"/wallets", bytes.NewBufferString(createReq))
	reqA.Header.Set("Authorization", "Bearer "+tokenA)
	reqA.Header.Set("Content-Type", "application/json")
	resA, err := http.DefaultClient.Do(reqA)
	require.NoError(t, err)
	defer resA.Body.Close()
	assert.Equal(t, http.StatusCreated, resA.StatusCode)

	// 2. Provedor B tentando processar transação em nome de Provedor A -> 403 Forbidden
	payload := `{
		"providerId": "provider-a",
		"externalTransactionId": "tx-isolation-1",
		"playerId": "p1",
		"walletId": "w1",
		"roundId": "r1",
		"gameId": "g1",
		"kind": "BET",
		"money": {"amount": "10.00", "currency": "BRL"}
	}`
	reqCross, _ := http.NewRequest("POST", server.URL+"/wagering/transactions", bytes.NewBufferString(payload))
	reqCross.Header.Set("Authorization", "Bearer "+tokenB) // Token B com providerId provider-a
	reqCross.Header.Set("Content-Type", "application/json")
	reqCross.Header.Set("Idempotency-Key", "provider-a:tx-isolation-1")

	resCross, err := http.DefaultClient.Do(reqCross)
	require.NoError(t, err)
	defer resCross.Body.Close()

	assert.Equal(t, http.StatusForbidden, resCross.StatusCode)
}

// 6. Teste de Resolução de Reversão Fora de Ordem (PENDING_REFERENCE -> Worker -> PROCESSED)
func TestIntegration_PendingReferenceResolution(t *testing.T) {
	server, repo, pool, token := setupTestApp(t)
	defer server.Close()
	defer pool.Close()

	playerID := uuid.NewString()

	// 1. Criar carteira com 100.00 BRL
	createReq := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"100.00","currency":"BRL"}}`, playerID)
	req, _ := http.NewRequest("POST", server.URL+"/wallets", bytes.NewBufferString(createReq))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	var walletResp struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&walletResp)
	walletID := walletResp.ID

	betExtID := "bet-ref-" + uuid.NewString()
	refundExtID := "refund-ref-" + uuid.NewString()

	// 2. Enviar REFUND ANTES da aposta existir
	refundPayload := fmt.Sprintf(`{
		"providerId": "provider-a",
		"externalTransactionId": "%s",
		"referenceExternalTransactionId": "%s",
		"playerId": "%s",
		"walletId": "%s",
		"roundId": "round-ref-1",
		"gameId": "game-slots",
		"kind": "REFUND",
		"money": {"amount": "30.00", "currency": "BRL"}
	}`, refundExtID, betExtID, playerID, walletID)

	reqRefund, _ := http.NewRequest("POST", server.URL+"/wagering/transactions", bytes.NewBufferString(refundPayload))
	reqRefund.Header.Set("Authorization", "Bearer "+token)
	reqRefund.Header.Set("Content-Type", "application/json")
	reqRefund.Header.Set("Idempotency-Key", "provider-a:"+refundExtID)

	resRefund, err := http.DefaultClient.Do(reqRefund)
	require.NoError(t, err)
	defer resRefund.Body.Close()

	assert.Equal(t, http.StatusOK, resRefund.StatusCode)
	var refundResp struct {
		TransactionID string `json:"transactionId"`
		Status        string `json:"status"`
	}
	_ = json.NewDecoder(resRefund.Body).Decode(&refundResp)
	assert.Equal(t, "PENDING_REFERENCE", refundResp.Status)

	// Saldo da carteira ainda deve ser 100.00 BRL
	reqGet, _ := http.NewRequest("GET", server.URL+"/wallets/"+walletID, nil)
	reqGet.Header.Set("Authorization", "Bearer "+token)
	resGet, _ := http.DefaultClient.Do(reqGet)
	var wCheck struct {
		Balance struct{ Amount string } `json:"balance"`
	}
	_ = json.NewDecoder(resGet.Body).Decode(&wCheck)
	resGet.Body.Close()
	assert.Equal(t, "100.00", wCheck.Balance.Amount)

	// 3. Agora a aposta (BET) chega e é processada
	betPayload := fmt.Sprintf(`{
		"providerId": "provider-a",
		"externalTransactionId": "%s",
		"playerId": "%s",
		"walletId": "%s",
		"roundId": "round-ref-1",
		"gameId": "game-slots",
		"kind": "BET",
		"money": {"amount": "30.00", "currency": "BRL"}
	}`, betExtID, playerID, walletID)

	reqBet, _ := http.NewRequest("POST", server.URL+"/wagering/transactions", bytes.NewBufferString(betPayload))
	reqBet.Header.Set("Authorization", "Bearer "+token)
	reqBet.Header.Set("Content-Type", "application/json")
	reqBet.Header.Set("Idempotency-Key", "provider-a:"+betExtID)

	resBet, err := http.DefaultClient.Do(reqBet)
	require.NoError(t, err)
	defer resBet.Body.Close()
	assert.Equal(t, http.StatusOK, resBet.StatusCode)

	// Saldo após aposta: 70.00 BRL
	resGet2, _ := http.DefaultClient.Do(reqGet)
	_ = json.NewDecoder(resGet2.Body).Decode(&wCheck)
	resGet2.Body.Close()
	assert.Equal(t, "70.00", wCheck.Balance.Amount)

	// 4. Inicia o PendingReferenceWorker para resolver pendências
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pendingWorker := messaging.NewPendingReferenceWorker(repo, logger)
	go pendingWorker.Start(workerCtx)

	// Aguarda até que o worker persista a resolução, com limite para evitar
	// que o teste dependa de um tempo fixo de execução.
	require.Eventually(t, func() bool {
		transaction, lookupErr := repo.GetTransactionByID(
			context.Background(),
			refundResp.TransactionID,
		)

		return lookupErr == nil &&
			transaction != nil &&
			transaction.Status() == domain.StatusProcessed
	}, 5*time.Second, 100*time.Millisecond)

	// 5. Verifica se a transação de REFUND foi resolvida para PROCESSED
	refundTx, err := repo.GetTransactionByID(context.Background(), refundResp.TransactionID)
	require.NoError(t, err)
	require.NotNil(t, refundTx)
	assert.Equal(t, domain.StatusProcessed, refundTx.Status())
	assert.NotEmpty(t, refundTx.ResolvedReferenceID())

	// Saldo final deve ter sido creditado de volta para 100.00 BRL
	resGet3, _ := http.DefaultClient.Do(reqGet)
	_ = json.NewDecoder(resGet3.Body).Decode(&wCheck)
	resGet3.Body.Close()
	// O saldo já foi validado como string decimal, sem conversão para float.
	assert.Equal(t, "100.00", wCheck.Balance.Amount)

	// Reconciliação deve confirmar integridade de 3 lançamentos (OPENING, BET, REFUND)
	reqRec, _ := http.NewRequest("POST", server.URL+"/wallets/"+walletID+"/reconciliation", nil)
	reqRec.Header.Set("Authorization", "Bearer "+token)
	resRec, err := http.DefaultClient.Do(reqRec)
	require.NoError(t, err)
	defer resRec.Body.Close()

	var recResp struct {
		Consistent     bool  `json:"consistent"`
		CheckedEntries int64 `json:"checkedEntries"`
	}
	_ = json.NewDecoder(resRec.Body).Decode(&recResp)
	assert.True(t, recResp.Consistent)
	assert.Equal(t, int64(3), recResp.CheckedEntries)
}

// 7. Teste do Consumidor SQS FIFO e Deduplicação via Inbox
func TestIntegration_SQSConsumerWorker(t *testing.T) {
	server, repo, pool, token := setupTestApp(t)
	defer server.Close()
	defer pool.Close()

	playerID := uuid.NewString()

	// 1. Criar carteira com 100.00 BRL
	createReq := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"100.00","currency":"BRL"}}`, playerID)
	req, _ := http.NewRequest("POST", server.URL+"/wallets", bytes.NewBufferString(createReq))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	var walletResp struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&walletResp)
	walletID := walletResp.ID

	// 2. Inicializar cliente SQS e Worker
	sqsConfig := messaging.Config{
		Region:    "us-east-1",
		Endpoint:  "http://localhost:4566",
		AccessKey: "test",
		SecretKey: "test",
		QueueURL:  "http://localhost:4566/000000000000/wager-transactions.fifo",
		DLQURL:    "http://localhost:4566/000000000000/wager-transactions-dlq.fifo",
	}
	sqsClient, err := messaging.NewSQSClient(sqsConfig)
	require.NoError(t, err)

	wagerService := application.NewWagerService(repo)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	consumerWorker := messaging.NewSQSConsumerWorker(sqsClient, sqsConfig, wagerService, repo, logger)

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	go consumerWorker.Start(workerCtx)

	// 3. Envia mensagem SQS FIFO
	msgID := "sqs-msg-" + uuid.NewString()
	extTxID := "ext-sqs-" + uuid.NewString()
	idempKey := "provider-a:" + extTxID

	sqsMsgBody := fmt.Sprintf(`{
		"messageId": "%s",
		"type": "WagerTransactionRequested",
		"occurredAt": "2026-09-14T22:00:00.000Z",
		"data": {
			"providerId": "provider-a",
			"externalTransactionId": "%s",
			"idempotencyKey": "%s",
			"playerId": "%s",
			"walletId": "%s",
			"roundId": "round-sqs-1",
			"gameId": "game-slots",
			"kind": "BET",
			"money": {"amount": "15.00", "currency": "BRL"}
		}
	}`, msgID, extTxID, idempKey, playerID, walletID)

	_, err = sqsClient.SendMessage(context.Background(), &sqs.SendMessageInput{
		QueueUrl:               aws.String(sqsConfig.QueueURL),
		MessageBody:            aws.String(sqsMsgBody),
		MessageGroupId:         aws.String("wallet-" + walletID),
		MessageDeduplicationId: aws.String(msgID),
	})
	require.NoError(t, err)

	// Aguarda até que o consumidor registre a mensagem na inbox, com limite
	// explícito para distinguir lentidão de uma falha real de processamento.
	require.Eventually(t, func() bool {
		processed, lookupErr := repo.HasInboxMessage(
			context.Background(),
			"wager-transactions-sqs-consumer",
			msgID,
		)

		return lookupErr == nil && processed
	}, 5*time.Second, 100*time.Millisecond)

	// 4. Valida se a mensagem foi registrada na Inbox e debitou a carteira para 85.00 BRL
	hasProcessed, err := repo.HasInboxMessage(context.Background(), "wager-transactions-sqs-consumer", msgID)
	require.NoError(t, err)
	assert.True(t, hasProcessed, "mensagem deve estar registrada na inbox_messages")

	reqGet, _ := http.NewRequest("GET", server.URL+"/wallets/"+walletID, nil)
	reqGet.Header.Set("Authorization", "Bearer "+token)
	resGet, _ := http.DefaultClient.Do(reqGet)
	var wCheck struct {
		Balance struct{ Amount string } `json:"balance"`
	}
	_ = json.NewDecoder(resGet.Body).Decode(&wCheck)
	resGet.Body.Close()
	assert.Equal(t, "85.00", wCheck.Balance.Amount)
}
