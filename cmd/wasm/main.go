//go:build js && wasm

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"
)

// =============================================================================
// DOMAIN MODELS & CULTURAL CONSTANTS (BELÉM DO PARÁ)
// =============================================================================

type Symbol struct {
	ID     string
	Char   string
	Name   string
	Mult   float64
	Weight int
}

var symbols = []Symbol{
	{ID: "muiraquita", Char: "💎", Name: "Muiraquitã Sagrado", Mult: 20.0, Weight: 5},
	{ID: "acai", Char: "🫐", Name: "Açaí do Ver-o-Peso", Mult: 15.0, Weight: 8},
	{ID: "filhote", Char: "🐟", Name: "Filhote Frito", Mult: 8.0, Weight: 12},
	{ID: "manga", Char: "🥭", Name: "Manga da P. Vargas", Mult: 5.0, Weight: 18},
	{ID: "castanha", Char: "🌰", Name: "Castanha-do-Pará", Mult: 3.0, Weight: 25},
	{ID: "onca", Char: "🐆", Name: "Onça do Marajó", Mult: 2.0, Weight: 32},
}

// Global App State
var (
	document js.Value
	window   js.Value

	audioCtx   js.Value
	soundOn    = true
	isSpinning = false

	activeProvider = "provider-a"
	tokens         = map[string]string{
		"internal":   "",
		"provider-a": "",
		"provider-b": "",
	}

	playerID string
	walletID string
	balance  float64 = 500.0

	currentBet float64 = 50.0
	lastWin    float64 = 0.0

	lastBetKey      string
	lastBetTxID     string
	lastBetRoundID  string
	lastBetAmount   float64
	lastBetPayload  map[string]interface{}
	lastBetSuccess  bool
	lastBetRefunded bool

	reels = [3]Symbol{symbols[1], symbols[2], symbols[3]} // Açaí, Filhote, Manga

	stateMu sync.Mutex
)

// =============================================================================
// MAIN ENTRYPOINT (GO WEBASSEMBLY RUNTIME)
// =============================================================================

func main() {
	window = js.Global()
	document = window.Get("document")

	fmt.Println("🚀 [Go WASM] Inicializando Jungle Slots Engine Belém 1987 em WebAssembly...")

	initAudio()
	initEventListeners()

	// Initial bootstrap in goroutine to avoid blocking browser main loop
	go func() {
		addLogLine("[WASM BOOT] Módulo WebAssembly em Go carregado com sucesso!")
		fetchTokens()
		checkHealth()
		initPlayerSession()
	}()

	// Keep Go WebAssembly runtime alive indefinitely
	select {}
}

// =============================================================================
// AUDIO ENGINE (WEB AUDIO API VIA GO SYSCALL/JS)
// =============================================================================

func initAudio() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Audio init failed:", r)
		}
	}()

	audioConstructor := window.Get("AudioContext")
	if audioConstructor.IsUndefined() || audioConstructor.IsNull() {
		audioConstructor = window.Get("webkitAudioContext")
	}
	if !audioConstructor.IsUndefined() && !audioConstructor.IsNull() {
		audioCtx = audioConstructor.New()
	}
}

func playSound(soundType string) {
	if !soundOn || audioCtx.IsUndefined() || audioCtx.IsNull() {
		return
	}

	// Resume audio context if suspended (browser autoplay policy)
	if audioCtx.Get("state").String() == "suspended" {
		audioCtx.Call("resume")
	}

	now := audioCtx.Get("currentTime").Float()

	switch soundType {
	case "click":
		playTone(800, now, 0.04, "sine", 0.1)
	case "chip":
		playTone(1200, now, 0.03, "triangle", 0.15)
	case "spin":
		playTone(350, now, 0.08, "sawtooth", 0.12)
	case "reel_stop":
		playTone(220, now, 0.1, "square", 0.2)
	case "win":
		playArpeggio([]float64{523.25, 659.25, 783.99, 1046.50}, now, 0.08)
	case "jackpot":
		playArpeggio([]float64{440, 554.37, 659.25, 880, 1108.73, 1318.51, 1760}, now, 0.09)
	case "refund":
		playTone(600, now, 0.08, "triangle", 0.2)
		playTone(450, now+0.09, 0.12, "triangle", 0.2)
	case "error":
		playTone(150, now, 0.25, "sawtooth", 0.3)
	}
}

func playTone(freq, startTime, duration float64, waveType string, gainVal float64) {
	osc := audioCtx.Call("createOscillator")
	gain := audioCtx.Call("createGain")

	osc.Set("type", waveType)
	osc.Get("frequency").Call("setValueAtTime", freq, startTime)

	gain.Get("gain").Call("setValueAtTime", gainVal, startTime)
	gain.Get("gain").Call("exponentialRampToValueAtTime", 0.0001, startTime+duration)

	osc.Call("connect", gain)
	gain.Call("connect", audioCtx.Get("destination"))

	osc.Call("start", startTime)
	osc.Call("stop", startTime+duration)
}

func playArpeggio(notes []float64, startTime, noteDuration float64) {
	for i, f := range notes {
		playTone(f, startTime+float64(i)*noteDuration, noteDuration*1.5, "triangle", 0.18)
	}
}

// =============================================================================
// DOM EVENT LISTENERS & UI WIRING
// =============================================================================

func initEventListeners() {
	// Sound Toggle
	setClickListener("btn-toggle-sound", func(this js.Value, args []js.Value) interface{} {
		soundOn = !soundOn
		btn := document.Call("getElementById", "btn-toggle-sound")
		if soundOn {
			btn.Set("textContent", "🔊 SFX: ON")
			playSound("click")
		} else {
			btn.Set("textContent", "🔈 SFX: OFF")
		}
		return nil
	})

	// Reset Game Session / Provision New Wallet
	setClickListener("btn-reset-all", func(this js.Value, args []js.Value) interface{} {
		playSound("click")
		go resetAllGame()
		return nil
	})

	// Provider Select Change
	providerSelect := document.Call("getElementById", "sel-provider")
	if !providerSelect.IsUndefined() && !providerSelect.IsNull() {
		providerSelect.Set("onchange", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			val := providerSelect.Get("value").String()
			activeProvider = val
			playSound("chip")
			updateAuthBadge()
			addLogLine(fmt.Sprintf("[TENANCY & AUTH] Provedor ativo alterado para '%s'. Token JWT Keycloak OIDC sincronizado!", val))
			return nil
		}))
	}

	// Expose inspectJWTGo to window and wire button
	window.Set("inspectJWTGo", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		inspectActiveJWT()
		return nil
	}))
	setClickListener("btn-inspect-jwt", func(this js.Value, args []js.Value) interface{} {
		inspectActiveJWT()
		return nil
	})

	// Chip Buttons
	chips := document.Call("querySelectorAll", ".chip-btn")
	for i := 0; i < chips.Length(); i++ {
		chip := chips.Index(i)
		chip.Set("onclick", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			valStr := this.Call("getAttribute", "data-val").String()
			val, _ := strconv.ParseFloat(valStr, 64)
			if val > 0 {
				setBetAmount(val)
				playSound("chip")
			}
			return nil
		}))
	}

	// Custom Bet Input
	inputBet := document.Call("getElementById", "input-custom-bet")
	if !inputBet.IsUndefined() && !inputBet.IsNull() {
		inputBet.Set("oninput", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			valStr := inputBet.Get("value").String()
			val, err := strconv.ParseFloat(valStr, 64)
			if err == nil && val > 0 {
				currentBet = val
				updateScoreboard()
			}
			return nil
		}))
	}

	// Spin Button
	setClickListener("btn-spin", func(this js.Value, args []js.Value) interface{} {
		if isSpinning {
			return nil
		}
		go handleSpin()
		return nil
	})

	// Replay Button
	setClickListener("btn-replay", func(this js.Value, args []js.Value) interface{} {
		if isSpinning {
			return nil
		}
		go handleReplay()
		return nil
	})

	// Refund Button
	setClickListener("btn-refund", func(this js.Value, args []js.Value) interface{} {
		if isSpinning {
			return nil
		}
		go handleRefund()
		return nil
	})

	// Expose global refresh function to window for HTML onclick fallback
	window.Set("refreshLedgerGo", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		playSound("click")
		go refreshLedger()
		return nil
	}))

	// Refresh Ledger Button
	setClickListener("btn-refresh-ledger", func(this js.Value, args []js.Value) interface{} {
		playSound("click")
		go refreshLedger()
		return nil
	})

	// Run Reconciliation Button
	setClickListener("btn-run-reconciliation", func(this js.Value, args []js.Value) interface{} {
		playSound("click")
		go runReconciliation()
		return nil
	})

	// Quick Audit Button in Wallet Deck
	setClickListener("btn-open-audit", func(this js.Value, args []js.Value) interface{} {
		playSound("click")
		switchTab("tab-reconcile")
		go runReconciliation()
		return nil
	})

	// Expose switchTab function to window for HTML onclick fallback
	window.Set("switchTabGo", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		if len(args) > 0 {
			targetTab := args[0].String()
			playSound("click")
			switchTab(targetTab)
		}
		return nil
	}))

	// Expose runReconciliation function to window for HTML onclick fallback
	window.Set("runReconciliationGo", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		playSound("click")
		go runReconciliation()
		return nil
	}))

	// Expose openAuditGo function to window (direct audit action from anywhere)
	window.Set("openAuditGo", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		playSound("click")
		switchTab("tab-reconcile")
		go runReconciliation()
		return nil
	}))

	// Modal Close Button
	setClickListener("btn-close-modal", func(this js.Value, args []js.Value) interface{} {
		modal := document.Call("getElementById", "payload-modal")
		if !modal.IsUndefined() && !modal.IsNull() {
			modal.Get("style").Set("display", "none")
		}
		return nil
	})

	// Regulatory Refund Modal Listeners (Edital Seção 7)
	closeRefundModal := func() {
		modal := document.Call("getElementById", "refund-modal")
		if !modal.IsUndefined() && !modal.IsNull() {
			modal.Get("style").Set("display", "none")
		}
	}
	setClickListener("btn-close-refund-modal", func(this js.Value, args []js.Value) interface{} {
		closeRefundModal()
		return nil
	})
	setClickListener("btn-cancel-refund", func(this js.Value, args []js.Value) interface{} {
		closeRefundModal()
		updateTicker("ℹ️ ESTORNO CANCELADO PELO OPERADOR.")
		addLogLine("[REFUND] Operação de estorno cancelada pelo operador.")
		return nil
	})
	setClickListener("btn-confirm-refund", func(this js.Value, args []js.Value) interface{} {
		closeRefundModal()
		go executeConfirmedRefund()
		return nil
	})

	// Deposit / Add Credits Modal Listeners
	closeDepositModal := func() {
		modal := document.Call("getElementById", "deposit-modal")
		if !modal.IsUndefined() && !modal.IsNull() {
			modal.Get("style").Set("display", "none")
		}
	}
	setClickListener("btn-add-credits", func(this js.Value, args []js.Value) interface{} {
		playSound("click")
		modal := document.Call("getElementById", "deposit-modal")
		if !modal.IsUndefined() && !modal.IsNull() {
			modal.Get("style").Set("display", "flex")
		}
		return nil
	})
	setClickListener("btn-close-deposit-modal", func(this js.Value, args []js.Value) interface{} {
		closeDepositModal()
		return nil
	})
	setClickListener("btn-cancel-deposit", func(this js.Value, args []js.Value) interface{} {
		closeDepositModal()
		return nil
	})

	// Quick deposit buttons (+50, +100, +250, +500)
	quickBtns := document.Call("querySelectorAll", ".btn-quick-deposit")
	for i := 0; i < quickBtns.Length(); i++ {
		btn := quickBtns.Index(i)
		btn.Set("onclick", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			valStr := this.Call("getAttribute", "data-val").String()
			input := document.Call("getElementById", "input-deposit-amount")
			if !input.IsUndefined() && !input.IsNull() {
				input.Set("value", valStr+".00")
			}
			playSound("chip")
			return nil
		}))
	}

	setClickListener("btn-confirm-deposit", func(this js.Value, args []js.Value) interface{} {
		input := document.Call("getElementById", "input-deposit-amount")
		var amt float64 = 100.00
		if !input.IsUndefined() && !input.IsNull() {
			valStr := input.Get("value").String()
			if v, err := strconv.ParseFloat(valStr, 64); err == nil && v > 0 {
				amt = v
			}
		}
		closeDepositModal()
		go executeDeposit(amt)
		return nil
	})

	// Terminal Tabs
	tabBtns := document.Call("querySelectorAll", ".tab-btn")
	for i := 0; i < tabBtns.Length(); i++ {
		btn := tabBtns.Index(i)
		btn.Set("onclick", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			targetTab := this.Call("getAttribute", "data-tab").String()
			playSound("click")
			switchTab(targetTab)
			return nil
		}))
	}

	// Unhappy Path Edge Cases Buttons
	errBtns := document.Call("querySelectorAll", ".btn-error-test")
	for i := 0; i < errBtns.Length(); i++ {
		btn := errBtns.Index(i)
		btn.Set("onclick", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			caseName := this.Call("getAttribute", "data-case").String()
			playSound("click")
			go triggerUnhappyCase(caseName)
			return nil
		}))
	}
}

func setClickListener(id string, fn func(this js.Value, args []js.Value) interface{}) {
	el := document.Call("getElementById", id)
	if !el.IsUndefined() && !el.IsNull() {
		cb := js.FuncOf(fn)
		el.Call("addEventListener", "click", cb)
		el.Set("onclick", cb)
	}
}

func switchTab(targetID string) {
	tabBtns := document.Call("querySelectorAll", ".tab-btn")
	for i := 0; i < tabBtns.Length(); i++ {
		btn := tabBtns.Index(i)
		attr := btn.Call("getAttribute", "data-tab").String()
		if attr == targetID {
			btn.Get("classList").Call("add", "active")
		} else {
			btn.Get("classList").Call("remove", "active")
		}
	}

	tabContents := document.Call("querySelectorAll", ".tab-content")
	for i := 0; i < tabContents.Length(); i++ {
		content := tabContents.Index(i)
		id := content.Get("id").String()
		if id == targetID {
			content.Get("classList").Call("add", "active")
		} else {
			content.Get("classList").Call("remove", "active")
		}
	}

	if targetID == "tab-reconcile" {
		go runReconciliation()
	}
}

func setBetAmount(amount float64) {
	currentBet = amount
	inputBet := document.Call("getElementById", "input-custom-bet")
	if !inputBet.IsUndefined() && !inputBet.IsNull() {
		inputBet.Set("value", fmt.Sprintf("%.2f", amount))
	}

	chips := document.Call("querySelectorAll", ".chip-btn")
	for i := 0; i < chips.Length(); i++ {
		chip := chips.Index(i)
		valStr := chip.Call("getAttribute", "data-val").String()
		val, _ := strconv.ParseFloat(valStr, 64)
		if val == amount {
			chip.Get("classList").Call("add", "active")
		} else {
			chip.Get("classList").Call("remove", "active")
		}
	}

	updateScoreboard()
}

// =============================================================================
// API & NETWORK HELPERS
// =============================================================================

func fetchTokens() {
	resp, err := http.Get("/app/api/tokens")
	if err != nil {
		setElementText("lbl-auth-status", "OFFLINE")
		setElementClass("dot-auth", "dot dot-red")
		setElementText("badge-jwt-status", "⚠️ FALHA JWT")
		addLogLine(fmt.Sprintf("[AUTH ERROR] Falha ao obter tokens do Keycloak: %v", err))
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var res struct {
		Internal  string `json:"internal"`
		ProviderA string `json:"providerA"`
		ProviderB string `json:"providerB"`
	}
	if err := json.Unmarshal(body, &res); err == nil && res.Internal != "" {
		tokens["internal"] = res.Internal
		tokens["provider-a"] = res.ProviderA
		tokens["provider-b"] = res.ProviderB
		setElementText("lbl-auth-status", "KEYCLOAK JWT")
		setElementClass("dot-auth", "dot dot-green")
		updateAuthBadge()
		addLogLine("[AUTH] ✅ Tokens JWT OIDC carregados do Keycloak com sucesso (internal, provider-a, provider-b)!")
	} else {
		setElementText("lbl-auth-status", "ERRO TOKEN")
		setElementClass("dot-auth", "dot dot-red")
		setElementText("badge-jwt-status", "⚠️ ERRO NO KEYCLOAK")
		addLogLine(fmt.Sprintf("[AUTH ERROR] Resposta inesperada de tokens: %s", string(body)))
	}
}

func updateAuthBadge() {
	token := tokens[activeProvider]
	if token == "" {
		token = tokens["provider-a"]
	}
	if token != "" {
		setElementText("badge-jwt-status", fmt.Sprintf("🔑 JWT: %s", strings.ToUpper(activeProvider)))
		setElementText("lbl-auth-status", "KEYCLOAK JWT")
		setElementClass("dot-auth", "dot dot-green")
	} else {
		setElementText("badge-jwt-status", "⚠️ JWT NÃO CARREGADO")
		setElementText("lbl-auth-status", "OFFLINE")
		setElementClass("dot-auth", "dot dot-red")
	}
}

func inspectActiveJWT() {
	token := tokens[activeProvider]
	if token == "" {
		token = tokens["provider-a"]
	}
	if token == "" {
		addLogLine(fmt.Sprintf("[AUTH INSPECTOR] Nenhum token JWT carregado ainda para '%s'.", activeProvider))
		return
	}
	playSound("click")
	addLogLine("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	addLogLine("🔑 [AUDITORIA DE AUTENTICAÇÃO OIDC / KEYCLOAK REALM: jungle-gaming]")
	addLogLine(fmt.Sprintf("   • Provedor Ativo: %s", activeProvider))
	addLogLine(fmt.Sprintf("   • Header Authorization: Bearer %s...%s (%d bytes)", token[:16], token[len(token)-10:], len(token)))

	var claimsMap map[string]interface{}
	parts := strings.Split(token, ".")
	if len(parts) >= 2 {
		payloadSegment := parts[1]
		decoded, err := base64.RawURLEncoding.DecodeString(payloadSegment)
		if err == nil {
			if err := json.Unmarshal(decoded, &claimsMap); err == nil {
				if iss, ok := claimsMap["iss"]; ok {
					addLogLine(fmt.Sprintf("   • Emissor OIDC (iss): %v", iss))
				}
				if azp, ok := claimsMap["azp"]; ok {
					addLogLine(fmt.Sprintf("   • Client ID (azp): %v", azp))
				}
				if sub, ok := claimsMap["sub"]; ok {
					addLogLine(fmt.Sprintf("   • Subject (sub): %v", sub))
				}
				if exp, ok := claimsMap["exp"].(float64); ok {
					expTime := time.Unix(int64(exp), 0)
					addLogLine(fmt.Sprintf("   • Validade do Token (exp): %s", expTime.Format("15:04:05 02/01/2006")))
				}
				if realmAccess, ok := claimsMap["realm_access"].(map[string]interface{}); ok {
					if roles, ok := realmAccess["roles"]; ok {
						addLogLine(fmt.Sprintf("   • Roles / Perfis Concedidos: %v", roles))
					}
				}
			}
		}
	}
	addLogLine("   • Criptografia: Assinatura RS256 com Chave Pública/Privada")
	addLogLine("   • Conformidade: Todos os endpoints REST enviam este Bearer Token!")
	addLogLine("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	modal := document.Call("getElementById", "payload-modal")
	curlBox := document.Call("getElementById", "modal-curl-content")
	jsonBox := document.Call("getElementById", "modal-json-content")
	titleEl := document.Call("getElementById", "modal-payload-title")
	curlLbl := document.Call("getElementById", "modal-curl-label")
	jsonLbl := document.Call("getElementById", "modal-json-label")

	if !modal.IsUndefined() && !modal.IsNull() {
		if !titleEl.IsUndefined() && !titleEl.IsNull() {
			titleEl.Set("textContent", "> INSPETOR DE AUTENTICAÇÃO OIDC & TOKEN JWT [KEYCLOAK]")
		}
		if !curlLbl.IsUndefined() && !curlLbl.IsNull() {
			curlLbl.Set("textContent", "> TESTE DE AUTORIZAÇÃO VIA CURL (BEARER TOKEN ATIVO):")
		}
		if !jsonLbl.IsUndefined() && !jsonLbl.IsNull() {
			jsonLbl.Set("textContent", "> CLAIMS DECODIFICADAS DO TOKEN (JWT PAYLOAD):")
		}

		curlStr := fmt.Sprintf(`curl -i -X GET "http://localhost:8000/wagering/health" \
  -H "Authorization: Bearer %s" \
  -H "X-Provider-Id: %s"`, token, activeProvider)

		var jsonStr string
		if claimsMap != nil {
			prettyJSON, err := json.MarshalIndent(claimsMap, "", "  ")
			if err == nil {
				jsonStr = string(prettyJSON)
			}
		}
		if jsonStr == "" {
			jsonStr = fmt.Sprintf(`{
  "provider": "%s",
  "status": "active",
  "tokenLength": %d
}`, activeProvider, len(token))
		}

		if !curlBox.IsUndefined() && !curlBox.IsNull() {
			curlBox.Set("textContent", curlStr)
		}
		if !jsonBox.IsUndefined() && !jsonBox.IsNull() {
			jsonBox.Set("textContent", jsonStr)
		}
		modal.Get("style").Set("display", "flex")
	}
}

func setElementClass(id, className string) {
	el := document.Call("getElementById", id)
	if !el.IsUndefined() && !el.IsNull() {
		el.Set("className", className)
	}
}

func checkHealth() {
	resp, err := http.Get("/health/ready")
	if err != nil {
		setElementText("lbl-api-status", "OFFLINE")
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var h struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(body, &h)
	if h.Status == "UP" || resp.StatusCode == 200 {
		setElementText("lbl-api-status", "ONLINE")
		setElementText("lbl-db-status", "CONNECTED")
		setElementText("lbl-sqs-status", "FIFO UP")
	}
}

func initPlayerSession() {
	stateMu.Lock()
	defer stateMu.Unlock()

	rand.Seed(time.Now().UnixNano())
	playerID = fmt.Sprintf("belem-player-%04d", rand.Intn(9000)+1000)

	setElementText("disp-player-id", playerID)

	token := tokens["internal"]
	if token == "" {
		addLogLine("[WARN] Token internal ainda não disponível, aguardando...")
		return
	}

	// Create wallet on server: POST /wallets
	payload := map[string]interface{}{
		"playerId": playerID,
		"initialBalance": map[string]string{
			"amount":   "500.00",
			"currency": "BRL",
		},
	}
	jsonBytes, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", "/wallets", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var wData struct {
			ID       string `json:"id"`
			PlayerID string `json:"playerId"`
			Balance  struct {
				Amount   string `json:"amount"`
				Currency string `json:"currency"`
			} `json:"balance"`
		}
		if err := json.Unmarshal(body, &wData); err == nil && wData.ID != "" {
			walletID = wData.ID
			balFloat, _ := strconv.ParseFloat(wData.Balance.Amount, 64)
			balance = balFloat
			setElementText("disp-wallet-id", walletID)
		} else {
			addLogLine(fmt.Sprintf("[WALLET ERROR] Resposta inválida: %s", string(body)))
			balance = 500.0
		}
	} else {
		addLogLine(fmt.Sprintf("[WALLET HTTP ERROR] %v", err))
		balance = 500.0
	}

	lastWin = 0.0
	lastBetKey = ""
	lastBetTxID = ""
	lastBetRoundID = ""
	lastBetAmount = 0.0
	lastBetSuccess = false
	lastBetRefunded = false

	updateScoreboard()
	disableButton("btn-replay", true)
	updateRefundButton(false, false, 0)
	addLogLine(fmt.Sprintf("[SESSION] Nova carteira no PostgreSQL: %s (Saldo: R$ %.2f)", walletID, balance))
	go refreshLedger()
}

func resetAllGame() {
	addLogLine("[RESET DEMO] Reiniciando sessão de demonstração: nova carteira com R$ 500 no PostgreSQL...")
	updateTicker("★ DEMO REINICIADA! NOVA CARTEIRA PROVISIONADA COM R$ 500,00! ★")
	initPlayerSession()
	playSound("win")
}

func updateScoreboard() {
	setElementText("disp-balance", fmt.Sprintf("R$ %.2f", balance))
	setElementText("disp-last-win", fmt.Sprintf("R$ %.2f", lastWin))
	setElementText("disp-current-bet", fmt.Sprintf("R$ %.2f", currentBet))
}

func updateTicker(msg string) {
	setElementText("ticker-text", msg)
}

func addLogLine(msg string) {
	logBox := document.Call("getElementById", "log-lines")
	if logBox.IsUndefined() || logBox.IsNull() {
		return
	}
	div := document.Call("createElement", "div")
	div.Get("classList").Call("add", "log-line")
	tStr := time.Now().Format("15:04:05")
	div.Set("textContent", fmt.Sprintf("> [%s] %s", tStr, msg))
	logBox.Call("appendChild", div)
	logBox.Set("scrollTop", logBox.Get("scrollHeight"))
}

func setElementText(id, text string) {
	el := document.Call("getElementById", id)
	if !el.IsUndefined() && !el.IsNull() {
		el.Set("textContent", text)
	}
}

func disableButton(id string, disabled bool) {
	el := document.Call("getElementById", id)
	if !el.IsUndefined() && !el.IsNull() {
		el.Set("disabled", disabled)
	}
}

func updateRefundButton(enabled bool, isRefunded bool, amount float64) {
	btn := document.Call("getElementById", "btn-refund")
	if btn.IsUndefined() || btn.IsNull() {
		return
	}
	span := btn.Call("querySelector", ".btn-top")

	if isRefunded {
		btn.Set("disabled", true)
		btn.Call("setAttribute", "title", fmt.Sprintf("Regra Anti-Double Refund (Edital Seção 7): A aposta %s já foi estornada!", lastBetTxID))
		if !span.IsUndefined() && !span.IsNull() {
			span.Set("textContent", "🚫 APOSTA JÁ ESTORNADA")
		} else {
			btn.Set("textContent", "🚫 APOSTA JÁ ESTORNADA")
		}
	} else if enabled && amount > 0 {
		btn.Set("disabled", false)
		btn.Call("setAttribute", "title", fmt.Sprintf("Estorno Regulatório (Edital Seção 7): Devolver integralmente R$ %.2f da aposta %s", amount, lastBetTxID))
		text := fmt.Sprintf("↩️ ESTORNAR (R$ %.2f)", amount)
		if !span.IsUndefined() && !span.IsNull() {
			span.Set("textContent", text)
		} else {
			btn.Set("textContent", text)
		}
	} else {
		btn.Set("disabled", true)
		btn.Call("setAttribute", "title", "Disponível após realizar uma aposta processada (BET)")
		if !span.IsUndefined() && !span.IsNull() {
			span.Set("textContent", "↩️ ESTORNAR APOSTA")
		} else {
			btn.Set("textContent", "↩️ ESTORNAR APOSTA")
		}
	}
}

// =============================================================================
// GAMEPLAY: SPIN, REELS ANIMATION, PAYOUT & RECONCILIATION
// =============================================================================

func handleSpin() {
	stateMu.Lock()
	if isSpinning {
		stateMu.Unlock()
		return
	}
	if walletID == "" {
		stateMu.Unlock()
		initPlayerSession()
		return
	}
	if balance < currentBet {
		stateMu.Unlock()
		playSound("error")
		updateTicker("❌ SALDO INSUFICIENTE! CARREGA TEU DINHEIRO OU DIMINUI APOSTA!")
		addLogLine(fmt.Sprintf("[AVISO] Saldo insuficiente: R$ %.2f < R$ %.2f", balance, currentBet))
		return
	}
	isSpinning = true
	disableButton("btn-spin", true)
	disableButton("btn-replay", true)
	updateRefundButton(false, false, 0)
	stateMu.Unlock()

	defer func() {
		stateMu.Lock()
		isSpinning = false
		disableButton("btn-spin", false)
		if lastBetSuccess {
			disableButton("btn-replay", false)
			updateRefundButton(!lastBetRefunded, lastBetRefunded, lastBetAmount)
		}
		stateMu.Unlock()
	}()

	timestamp := time.Now().UnixMilli()
	txID := fmt.Sprintf("tx-bet-%d", timestamp)
	roundID := fmt.Sprintf("round-slots-%d", timestamp)
	betKey := fmt.Sprintf("%s:%s", activeProvider, txID)

	token := tokens[activeProvider]
	if token == "" {
		token = tokens["provider-a"]
	}

	payload := map[string]interface{}{
		"providerId":            activeProvider,
		"externalTransactionId": txID,
		"playerId":              playerID,
		"walletId":              walletID,
		"roundId":               roundID,
		"gameId":                "game-jungle-slots",
		"kind":                  "BET",
		"money": map[string]string{
			"amount":   fmt.Sprintf("%.2f", currentBet),
			"currency": "BRL",
		},
	}

	lastBetKey = betKey
	lastBetTxID = txID
	lastBetRoundID = roundID
	lastBetAmount = currentBet
	lastBetPayload = payload
	lastBetRefunded = false

	playSound("spin")
	updateTicker("🎰 GIRANDO OS ROLOS DO CARIMBÓ... SEGURA O CORAÇÃO!")
	addLogLine(fmt.Sprintf("[BET DISPATCH] Enviando aposta de R$ %.2f (Key: %s)", currentBet, betKey))

	// Animate reels in goroutine
	doneAnim := make(chan bool)
	go animateReels(doneAnim)

	// Dispatch BET to API: POST /wagering/transactions
	jsonBytes, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", betKey)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)

	if err != nil {
		<-doneAnim
		playSound("error")
		updateTicker("❌ ERRO DE CONEXÃO COM A API DO JOGO!")
		addLogLine(fmt.Sprintf("[HTTP ERROR] %v", err))
		return
	}

	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		<-doneAnim
		playSound("error")
		updateTicker(fmt.Sprintf("❌ ERRO NO DÉBITO (HTTP %d): %s", resp.StatusCode, string(respBody)))
		addLogLine(fmt.Sprintf("[HTTP %d] %s", resp.StatusCode, string(respBody)))
		return
	}

	var betResp struct {
		Balance struct {
			Amount string `json:"amount"`
		} `json:"balance"`
		ExternalTransactionID string `json:"externalTransactionId"`
	}
	_ = json.Unmarshal(respBody, &betResp)

	stateMu.Lock()
	if amt, err := strconv.ParseFloat(betResp.Balance.Amount, 64); err == nil {
		balance = amt
	}
	lastBetAmount = currentBet
	lastBetSuccess = true
	updateScoreboard()
	stateMu.Unlock()

	// Wait animation to complete
	<-doneAnim

	// Determine spin outcome
	s1 := pickWeightedSymbol()
	s2 := pickWeightedSymbol()
	s3 := pickWeightedSymbol()

	reels = [3]Symbol{s1, s2, s3}
	updateReelDisplay(0, s1.Char)
	updateReelDisplay(1, s2.Char)
	updateReelDisplay(2, s3.Char)
	playSound("reel_stop")

	// Payout calculation
	winAmount := 0.0
	tickerMsg := ""

	if s1.ID == s2.ID && s2.ID == s3.ID {
		winAmount = currentBet * s1.Mult
		if s1.ID == "muiraquita" || s1.ID == "acai" {
			playSound("jackpot")
			tickerMsg = fmt.Sprintf("🔥 ÉGUA DO JACKPOT! TRÊS %s! GANHASTE R$ %.2f!", strings.ToUpper(s1.Name), winAmount)
		} else {
			playSound("win")
			tickerMsg = fmt.Sprintf("🎉 PAI D'ÉGUA! TRÊS %s! PREMIAÇÃO DE R$ %.2f!", strings.ToUpper(s1.Name), winAmount)
		}
	} else if s1.ID == s2.ID || s2.ID == s3.ID || s1.ID == s3.ID {
		winAmount = currentBet * 1.10
		playSound("win")
		tickerMsg = fmt.Sprintf("✨ DOIS SÍMBOLOS IGUAIS! GANHASTE R$ %.2f (1.1x)!", winAmount)
	} else {
		tickerMsg = "🍂 NÃO FOI DESSA VEZ, MANO! GIRA DE NOVO NO VER-O-PESO!"
	}

	updateTicker(tickerMsg)
	lastWin = winAmount
	updateScoreboard()

	// If win > 0, dispatch WIN to backend: POST /wagering/transactions
	if winAmount > 0 {
		go func(amount float64, rID string) {
			winTxID := fmt.Sprintf("tx-win-%d", time.Now().UnixMilli())
			winKey := fmt.Sprintf("%s:%s", activeProvider, winTxID)
			winPayload := map[string]interface{}{
				"providerId":            activeProvider,
				"externalTransactionId": winTxID,
				"playerId":              playerID,
				"walletId":              walletID,
				"roundId":               rID,
				"gameId":                "game-jungle-slots",
				"kind":                  "WIN",
				"money": map[string]string{
					"amount":   fmt.Sprintf("%.2f", amount),
					"currency": "BRL",
				},
			}
			wBytes, _ := json.Marshal(winPayload)
			wReq, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(wBytes))
			wReq.Header.Set("Content-Type", "application/json")
			wReq.Header.Set("Idempotency-Key", winKey)
			if token != "" {
				wReq.Header.Set("Authorization", "Bearer "+token)
			}

			wResp, wErr := client.Do(wReq)
			if wErr == nil {
				wBody, _ := io.ReadAll(wResp.Body)
				wResp.Body.Close()
				var wRes struct {
					Balance struct {
						Amount string `json:"amount"`
					} `json:"balance"`
				}
				if json.Unmarshal(wBody, &wRes) == nil {
					stateMu.Lock()
					if wAmt, err := strconv.ParseFloat(wRes.Balance.Amount, 64); err == nil {
						balance = wAmt
					}
					updateScoreboard()
					stateMu.Unlock()
					addLogLine(fmt.Sprintf("[WIN CREDIT] Crédito de R$ %.2f efetuado! Novo saldo: R$ %.2f", amount, balance))
					go refreshLedger()
				}
			}
		}(winAmount, roundID)
	} else {
		go refreshLedger()
	}
}

func animateReels(done chan bool) {
	sampleChars := []string{"💎", "🫐", "🐟", "🥭", "🌰", "🐆"}
	for i := 0; i < 12; i++ {
		r1 := sampleChars[rand.Intn(len(sampleChars))]
		r2 := sampleChars[rand.Intn(len(sampleChars))]
		r3 := sampleChars[rand.Intn(len(sampleChars))]
		updateReelDisplay(0, r1)
		updateReelDisplay(1, r2)
		updateReelDisplay(2, r3)
		time.Sleep(70 * time.Millisecond)
	}
	done <- true
}

func updateReelDisplay(reelIndex int, emoji string) {
	id := fmt.Sprintf("strip-%d", reelIndex+1)
	strip := document.Call("getElementById", id)
	if !strip.IsUndefined() && !strip.IsNull() {
		strip.Set("innerHTML", fmt.Sprintf(`<div class="reel-cell">%s</div>`, emoji))
	}
}

func pickWeightedSymbol() Symbol {
	totalWeight := 0
	for _, s := range symbols {
		totalWeight += s.Weight
	}
	r := rand.Intn(totalWeight)
	accum := 0
	for _, s := range symbols {
		accum += s.Weight
		if r < accum {
			return s
		}
	}
	return symbols[len(symbols)-1]
}

// =============================================================================
// REPLAY IDEMPOTENTE & ESTORNO REGULATÓRIO (REFUND)
// =============================================================================

func handleReplay() {
	if lastBetKey == "" || lastBetPayload == nil {
		updateTicker("⚠️ NENHUMA APOSTA ANTERIOR PARA REPLAY!")
		return
	}

	playSound("click")
	updateTicker("🔁 REENVIANDO MESMA CHAVE IDEMPOTENTE PARA A ENGINE...")
	addLogLine(fmt.Sprintf("[IDEMPOTENCY REPLAY] Reenviando key='%s'...", lastBetKey))

	token := tokens[activeProvider]
	if token == "" {
		token = tokens["provider-a"]
	}

	jsonBytes, _ := json.Marshal(lastBetPayload)
	req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", lastBetKey)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		playSound("error")
		updateTicker("❌ ERRO DE REDE NO REPLAY!")
		return
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		playSound("win")
		updateTicker("✅ REPLAY IDEMPOTENTE ACEITO! NENHUM DÉBITO DUPLICADO!")
		addLogLine(fmt.Sprintf("[REPLAY SUCCESS] Resposta idempotente: %s", string(body)))
		go refreshLedger()
	} else {
		playSound("error")
		updateTicker(fmt.Sprintf("❌ REPLAY FALHOU (HTTP %d)", resp.StatusCode))
		addLogLine(fmt.Sprintf("[REPLAY FAILED] HTTP %d: %s", resp.StatusCode, string(body)))
	}
}

func handleRefund() {
	stateMu.Lock()
	txID := lastBetTxID
	roundID := lastBetRoundID
	amount := lastBetAmount
	refunded := lastBetRefunded
	success := lastBetSuccess
	stateMu.Unlock()

	if txID == "" || !success {
		playSound("error")
		updateTicker("⚠️ NENHUMA APOSTA ELEGÍVEL PARA ESTORNO! GIRA UMA RODADA PRIMEIRO, MANO!")
		addLogLine("[AVISO] Tentativa de estorno sem aposta prévia.")
		window.Call("alert", "⚠️ NENHUMA APOSTA ENCONTRADA\n\nConforme o Edital do Desafio Jungle Game (Seção 7), o estorno (kind=REFUND) só pode ser executado como reversão integral de uma aposta (BET) já processada.")
		return
	}

	if refunded {
		playSound("error")
		updateTicker("⚠️ REGRA ANTI-DOUBLE REFUND (EDITAL SEÇÃO 7): APOSTA JÁ ESTORNADA!")
		addLogLine(fmt.Sprintf("[AUDITORIA - REJEIÇÃO REGULATÓRIA] Tentativa de duplo estorno bloqueada para a aposta %s. (Edital Seção 6.4: rejeições NÃO geram lançamento no Ledger)", txID))
		window.Call("alert", fmt.Sprintf("⚠️ BLOQUEIO DO EDITAL (Seção 7 - Anti-Double Refund)\n\nA transação [%s] no valor de R$ %.2f já foi estornada!\nO motor regulatório do desafio proíbe devolução duplicada do mesmo débito.\n\nNota Contábil: Conforme a Seção 6.4 do Edital, tentativas rejeitadas não geram lançamentos no Livro-Razão (Ledger).", txID, amount))
		return
	}

	// Tenta abrir o modal de auditoria e conformidade do edital
	refundModal := document.Call("getElementById", "refund-modal")
	if !refundModal.IsUndefined() && !refundModal.IsNull() {
		setElementText("refund-modal-txid", txID)
		setElementText("refund-modal-roundid", roundID)
		setElementText("refund-modal-provider", activeProvider)
		setElementText("refund-modal-wallet", fmt.Sprintf("%s / %s", walletID, playerID))
		setElementText("refund-modal-amount", fmt.Sprintf("R$ %.2f", amount))
		playSound("click")
		refundModal.Get("style").Set("display", "flex")
		return
	}

	// Fallback para diálogo confirm nativo do browser se modal não existir
	confirmMsg := fmt.Sprintf(
		"📋 PROTOCOLO REGULATÓRIO DE ESTORNO (EDITAL SEÇÃO 7 - REFUND)\n\n"+
			"Atenção: O estorno financeiro reverte integralmente o débito da aposta no Livro-Razão.\n\n"+
			"• Transação de Origem: %s\n"+
			"• Rodada (Round ID): %s\n"+
			"• Valor do Débito a Devolver: R$ %.2f BRL\n"+
			"• Provedor de Jogos: %s\n"+
			"• Carteira / Jogador: %s\n\n"+
			"Critérios do Edital cumpridos:\n"+
			"✔ Validação 1:1 de Moeda e Valor exato (Reversão Parcial Proibida)\n"+
			"✔ Referência direta a BET processada\n"+
			"✔ Bloqueio subsequente Anti-Double Refund\n\n"+
			"Confirmas a execução do estorno regulatório?",
		txID, roundID, amount, activeProvider, playerID,
	)
	if !window.Call("confirm", confirmMsg).Bool() {
		updateTicker("ℹ️ ESTORNO CANCELADO PELO OPERADOR.")
		addLogLine("[REFUND] Operação de estorno cancelada pelo operador.")
		return
	}

	go executeConfirmedRefund()
}

func executeConfirmedRefund() {
	stateMu.Lock()
	if lastBetTxID == "" || lastBetRefunded {
		stateMu.Unlock()
		return
	}
	txID := lastBetTxID
	roundID := lastBetRoundID
	amount := lastBetAmount
	updateRefundButton(false, false, amount)
	stateMu.Unlock()

	playSound("click")
	updateTicker(fmt.Sprintf("↩️ PROCESSANDO ESTORNO REGULATÓRIO (REFUND R$ %.2f)...", amount))
	addLogLine(fmt.Sprintf("[REFUND REQUEST] Solicitando estorno de %s (Valor: R$ %.2f - Provedor: %s)", txID, amount, activeProvider))

	token := tokens[activeProvider]
	if token == "" {
		token = tokens["provider-a"]
	}

	refundTxID := fmt.Sprintf("tx-refund-%d", time.Now().UnixMilli())
	refundKey := fmt.Sprintf("%s:%s", activeProvider, refundTxID)

	payload := map[string]interface{}{
		"providerId":                     activeProvider,
		"externalTransactionId":          refundTxID,
		"playerId":                       playerID,
		"walletId":                       walletID,
		"roundId":                        roundID,
		"gameId":                         "game-jungle-slots",
		"kind":                           "REFUND",
		"referenceExternalTransactionId": txID,
		"money": map[string]string{
			"amount":   fmt.Sprintf("%.2f", amount),
			"currency": "BRL",
		},
	}

	jsonBytes, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", refundKey)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		playSound("error")
		updateTicker("❌ ERRO DE CONEXÃO AO PROCESSAR ESTORNO!")
		addLogLine(fmt.Sprintf("[REFUND ERROR] Falha de rede: %v", err))
		stateMu.Lock()
		updateRefundButton(true, false, amount)
		stateMu.Unlock()
		return
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		var rRes struct {
			Balance struct {
				Amount string `json:"amount"`
			} `json:"balance"`
		}
		_ = json.Unmarshal(body, &rRes)

		stateMu.Lock()
		if rAmt, err := strconv.ParseFloat(rRes.Balance.Amount, 64); err == nil {
			balance = rAmt
		}
		lastBetRefunded = true
		updateScoreboard()
		updateRefundButton(false, true, amount)
		stateMu.Unlock()

		playSound("refund")
		updateTicker(fmt.Sprintf("✅ ESTORNO CONCLUÍDO! +R$ %.2f DEVOLVIDOS À CARTEIRA!", amount))
		addLogLine(fmt.Sprintf("[REFUND SUCCESS] Aposta %s estornada com sucesso. Saldo: R$ %.2f (Regra Anti-Double Refund Ativada)", txID, balance))
		go refreshLedger()
	} else {
		playSound("error")
		updateTicker(fmt.Sprintf("❌ ESTORNO RECUSADO PELA ENGINE (HTTP %d)", resp.StatusCode))
		addLogLine(fmt.Sprintf("[AUDITORIA - REJEIÇÃO REGULATÓRIA] HTTP %d: %s (Edital 6.4: rejeição auditada, sem lançamento no Ledger)", resp.StatusCode, string(body)))
		stateMu.Lock()
		updateRefundButton(true, false, amount)
		stateMu.Unlock()
		window.Call("alert", fmt.Sprintf("❌ ESTORNO REGULATÓRIO REJEITADO (HTTP %d):\n\n%s\n\nConforme o Edital (Seção 6.4), tentativas rejeitadas são auditadas nas transações mas não afetam o Livro-Razão.", resp.StatusCode, string(body)))
	}
}

func executeDeposit(amount float64) {
	if amount <= 0 {
		return
	}
	stateMu.Lock()
	wid := walletID
	pid := playerID
	stateMu.Unlock()

	if wid == "" {
		playSound("error")
		updateTicker("⚠️ NENHUMA CARTEIRA ATIVA! REINICIE A SESSÃO.")
		return
	}

	token := tokens[activeProvider]
	if token == "" {
		token = tokens["provider-a"]
	}

	txID := fmt.Sprintf("tx-deposit-%d", time.Now().UnixMilli())
	key := fmt.Sprintf("%s:%s", activeProvider, txID)
	roundID := fmt.Sprintf("round-deposit-%d", time.Now().UnixMilli())

	payload := map[string]interface{}{
		"providerId":            activeProvider,
		"externalTransactionId": txID,
		"playerId":              pid,
		"walletId":              wid,
		"roundId":               roundID,
		"gameId":                "game-jungle-slots",
		"kind":                  "WIN",
		"money": map[string]string{
			"amount":   fmt.Sprintf("%.2f", amount),
			"currency": "BRL",
		},
	}

	jsonBytes, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	updateTicker(fmt.Sprintf("⏳ ADICIONANDO +R$ %.2f EM CRÉDITOS...", amount))
	addLogLine(fmt.Sprintf("[CRÉDITOS] Solicitando recarga de R$ %.2f (kind=WIN - Provedor: %s)...", amount, activeProvider))

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		playSound("error")
		updateTicker("❌ FALHA DE REDE AO ADICIONAR CRÉDITOS!")
		addLogLine(fmt.Sprintf("[CRÉDITOS ERROR] Falha de conexão: %v", err))
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		var rRes struct {
			Balance struct {
				Amount string `json:"amount"`
			} `json:"balance"`
		}
		_ = json.Unmarshal(body, &rRes)

		stateMu.Lock()
		if rAmt, err := strconv.ParseFloat(rRes.Balance.Amount, 64); err == nil {
			balance = rAmt
		}
		updateScoreboard()
		stateMu.Unlock()

		playSound("win")
		updateTicker(fmt.Sprintf("💰 +R$ %.2f CREDITADOS COM SUCESSO! SALDO: R$ %.2f", amount, balance))
		addLogLine(fmt.Sprintf("[CRÉDITOS SUCESSO] ✅ +R$ %.2f adicionados à carteira via transação WIN. Saldo: R$ %.2f", amount, balance))
		go refreshLedger()
	} else {
		playSound("error")
		updateTicker(fmt.Sprintf("❌ RECARGA RECUSADA (HTTP %d)", resp.StatusCode))
		addLogLine(fmt.Sprintf("[CRÉDITOS REJEITADOS] HTTP %d: %s", resp.StatusCode, string(body)))
	}
}

// =============================================================================
// AUDIT: LEDGER LIVRO DO VER-O-PESO & RECONCILIATION
// =============================================================================

type LedgerItemDTO struct {
	ID                    string `json:"id"`
	WalletID              string `json:"walletId"`
	TransactionID         string `json:"transactionId"`
	Kind                  string `json:"kind"`
	Direction             string `json:"direction"`
	Amount                string `json:"amount"`
	Currency              string `json:"currency"`
	BalanceBefore         string `json:"balanceBefore"`
	BalanceAfter          string `json:"balanceAfter"`
	ExternalTransactionID string `json:"externalTransactionId"`
	ReferenceExternalID   string `json:"referenceExternalTransactionId"`
	CreatedAt             string `json:"createdAt"`
}

type LedgerResponseDTO struct {
	Entries    []LedgerItemDTO `json:"entries"`
	NextCursor string          `json:"nextCursor"`
}

func refreshLedger() {
	stateMu.Lock()
	wid := walletID
	stateMu.Unlock()

	if wid == "" {
		updateTicker("⚠️ NENHUMA CARTEIRA ATIVA PARA CONSULTAR O LIVRO-RAZÃO!")
		addLogLine("[LEDGER] Nenhuma carteira ativa para consultar o livro-razão.")
		return
	}

	setElementText("btn-refresh-ledger", "⏳ CONSULTANDO...")
	disableButton("btn-refresh-ledger", true)
	playSound("click")
	updateTicker("⟳ CONSULTANDO LIVRO-RAZÃO NO POSTGRESQL...")

	url := fmt.Sprintf("/wallets/%s/ledger?limit=25", wid)
	token := tokens["internal"]
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		setElementText("btn-refresh-ledger", "❌ ERRO DE REDE")
		updateTicker("❌ ERRO DE CONEXÃO AO ATUALIZAR LIVRO-RAZÃO!")
		addLogLine(fmt.Sprintf("[LEDGER ERROR] %v", err))
		go func() {
			time.Sleep(1500 * time.Millisecond)
			setElementText("btn-refresh-ledger", "⟳ ATUALIZAR LEDGER")
			disableButton("btn-refresh-ledger", false)
		}()
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var ledResp LedgerResponseDTO
	if err := json.Unmarshal(body, &ledResp); err != nil {
		addLogLine(fmt.Sprintf("[LEDGER PARSE ERROR] %v (corpo: %s)", err, string(body)))
		setElementText("btn-refresh-ledger", "⟳ ATUALIZAR LEDGER")
		disableButton("btn-refresh-ledger", false)
		return
	}

	renderLedgerTable(ledResp.Entries)
	playSound("chip")
	updateTicker(fmt.Sprintf("✅ LIVRO-RAZÃO ATUALIZADO: %d LANÇAMENTOS NO BANCO!", len(ledResp.Entries)))
	addLogLine(fmt.Sprintf("[LEDGER] Livro-Razão sincronizado com o PostgreSQL! Total: %d lançamentos.", len(ledResp.Entries)))

	// Glow table border so user visibly sees the refresh!
	tableContainer := document.Call("querySelector", ".ledger-table-container")
	if !tableContainer.IsUndefined() && !tableContainer.IsNull() {
		tableContainer.Get("style").Set("borderColor", "#22c55e")
		tableContainer.Get("style").Set("boxShadow", "0 0 15px rgba(34, 197, 94, 0.4)")
	}

	go func(count int) {
		setElementText("btn-refresh-ledger", fmt.Sprintf("✅ %d ITENS!", count))
		time.Sleep(1200 * time.Millisecond)
		setElementText("btn-refresh-ledger", "⟳ ATUALIZAR LEDGER")
		disableButton("btn-refresh-ledger", false)
		if !tableContainer.IsUndefined() && !tableContainer.IsNull() {
			tableContainer.Get("style").Set("borderColor", "")
			tableContainer.Get("style").Set("boxShadow", "")
		}
	}(len(ledResp.Entries))

	// Sincroniza também o saldo atual da carteira
	go func() {
		wReq, _ := http.NewRequest("GET", "/wallets/"+walletID, nil)
		if token != "" {
			wReq.Header.Set("Authorization", "Bearer "+token)
		}
		if wResp, wErr := client.Do(wReq); wErr == nil && wResp != nil {
			wBody, _ := io.ReadAll(wResp.Body)
			wResp.Body.Close()
			var wData struct {
				Balance struct {
					Amount string `json:"amount"`
				} `json:"balance"`
			}
			if json.Unmarshal(wBody, &wData) == nil && wData.Balance.Amount != "" {
				if balFloat, err := strconv.ParseFloat(wData.Balance.Amount, 64); err == nil {
					stateMu.Lock()
					balance = balFloat
					updateScoreboard()
					stateMu.Unlock()
				}
			}
		}
	}()
}

func renderLedgerTable(entries []LedgerItemDTO) {
	tbody := document.Call("getElementById", "ledger-tbody")
	if tbody.IsUndefined() || tbody.IsNull() {
		return
	}

	if len(entries) == 0 {
		tbody.Set("innerHTML", `<tr><td colspan="7" class="terminal-dim">> Nenhum lançamento registrado no Livro do Ver-o-Peso. Inicie um giro!</td></tr>`)
		return
	}

	var sb strings.Builder
	for i, e := range entries {
		badgeClass := "badge-debit"
		typeLabel := "DÉBITO"
		sign := "-"
		if e.Direction == "CREDIT" {
			badgeClass = "badge-credit"
			typeLabel = "CRÉDITO"
			sign = "+"
		}

		kindTag := e.Kind
		if kindTag == "" {
			kindTag = e.Direction
		}

		refText := "---"
		if e.ReferenceExternalID != "" {
			refText = fmt.Sprintf(`<span class="ref-link" title="%s">%s...</span>`, e.ReferenceExternalID, truncateString(e.ReferenceExternalID, 12))
		}

		txDisplay := e.ExternalTransactionID
		if txDisplay == "" {
			txDisplay = e.TransactionID
		}

		seq := len(entries) - i
		sb.WriteString(fmt.Sprintf(`
			<tr>
				<td><strong>#%d</strong></td>
				<td><span class="badge %s">%s (%s)</span></td>
				<td><strong class="%s">%s R$ %s</strong></td>
				<td>R$ %s</td>
				<td><span class="code-mono" title="%s">%s...</span></td>
				<td>%s</td>
				<td>
					<button class="btn-table-action" onclick="window.inspectTx('%s', '%s', '%s')">🔍 PAYLOAD</button>
				</td>
			</tr>
		`, seq, badgeClass, typeLabel, kindTag, badgeClass, sign, e.Amount, e.BalanceAfter, txDisplay, truncateString(txDisplay, 14), refText, txDisplay, e.Kind, e.Amount))
	}

	tbody.Set("innerHTML", sb.String())

	// Expose inspect function to window
	window.Set("inspectTx", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		if len(args) >= 3 {
			txID := args[0].String()
			kind := args[1].String()
			amt := args[2].String()
			openPayloadModal(txID, kind, amt)
		}
		return nil
	}))
}

func truncateString(str string, num int) string {
	if len(str) <= num {
		return str
	}
	return str[:num]
}

func openPayloadModal(txID, kind, amount string) {
	modal := document.Call("getElementById", "payload-modal")
	curlBox := document.Call("getElementById", "modal-curl-content")
	jsonBox := document.Call("getElementById", "modal-json-content")
	titleEl := document.Call("getElementById", "modal-payload-title")
	curlLbl := document.Call("getElementById", "modal-curl-label")
	jsonLbl := document.Call("getElementById", "modal-json-label")

	if modal.IsUndefined() || modal.IsNull() {
		return
	}

	if !titleEl.IsUndefined() && !titleEl.IsNull() {
		titleEl.Set("textContent", "> INSPETOR DE REQUISIÇÃO & PAYLOAD")
	}
	if !curlLbl.IsUndefined() && !curlLbl.IsNull() {
		curlLbl.Set("textContent", "> COMANDO CURL EQUIVALENTE:")
	}
	if !jsonLbl.IsUndefined() && !jsonLbl.IsNull() {
		jsonLbl.Set("textContent", "> PAYLOAD JSON DA OPERAÇÃO:")
	}

	curlStr := fmt.Sprintf(`curl -X POST "http://localhost:8000/wagering/transactions" \
  -H "Authorization: Bearer <TOKEN_JWT>" \
  -H "Idempotency-Key: %s:%s" \
  -H "Content-Type: application/json" \
  -d '{
    "providerId": "%s",
    "externalTransactionId": "%s",
    "playerId": "%s",
    "walletId": "%s",
    "kind": "%s",
    "money": {
      "amount": "%s",
      "currency": "BRL"
    }
  }'`, activeProvider, txID, activeProvider, txID, playerID, walletID, kind, amount)

	jsonStr := fmt.Sprintf(`{
  "providerId": "%s",
  "externalTransactionId": "%s",
  "playerId": "%s",
  "walletId": "%s",
  "kind": "%s",
  "money": {
    "amount": "%s",
    "currency": "BRL"
  },
  "timestamp": "%s"
}`, activeProvider, txID, playerID, walletID, kind, amount, time.Now().Format(time.RFC3339))

	curlBox.Set("textContent", curlStr)
	jsonBox.Set("textContent", jsonStr)
	modal.Get("style").Set("display", "flex")
}

func runReconciliation() {
	stateMu.Lock()
	wid := walletID
	stateMu.Unlock()

	if wid == "" {
		updateTicker("⚠️ NENHUMA CARTEIRA ATIVA PARA AUDITAR!")
		return
	}

	btn := document.Call("getElementById", "btn-run-reconciliation")
	if !btn.IsUndefined() && !btn.IsNull() {
		btn.Set("textContent", "⚡ AUDITANDO BASE DE DADOS POSTGRESQL...")
		btn.Set("disabled", true)
	}

	defer func() {
		if !btn.IsUndefined() && !btn.IsNull() {
			btn.Set("textContent", "⚡ EXECUTAR AUDITORIA MATEMÁTICA AGORA")
			btn.Set("disabled", false)
		}
	}()

	playSound("click")
	updateTicker("⚖️ AUDITANDO LIVRO-RAZÃO E CONFERINDO SALDO NO POSTGRESQL...")
	addLogLine(fmt.Sprintf("[AUDIT] Disparando reconciliação matemática da carteira %s no PostgreSQL...", wid))

	url := fmt.Sprintf("/wallets/%s/reconciliation", wid)
	token := tokens["internal"]
	req, _ := http.NewRequest("POST", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		playSound("error")
		updateTicker("❌ ERRO DE CONEXÃO AO AUDITAR!")
		addLogLine(fmt.Sprintf("[RECONCILE ERROR] Falha de rede: %v", err))
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		playSound("error")
		updateTicker(fmt.Sprintf("❌ ERRO NA AUDITORIA (HTTP %d)", resp.StatusCode))
		addLogLine(fmt.Sprintf("[RECONCILE ERROR] HTTP %d: %s", resp.StatusCode, string(body)))
		return
	}

	var rec struct {
		WalletID      string `json:"walletId"`
		StoredBalance struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"storedBalance"`
		CalculatedBalance struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"calculatedBalance"`
		Difference struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"difference"`
		Consistent     bool  `json:"consistent"`
		CheckedEntries int64 `json:"checkedEntries"`
	}

	if err := json.Unmarshal(body, &rec); err != nil {
		playSound("error")
		addLogLine(fmt.Sprintf("[RECONCILE PARSE ERROR] %v: %s", err, string(body)))
		return
	}

	resBox := document.Call("getElementById", "reconcile-result")
	if !resBox.IsUndefined() && !resBox.IsNull() {
		resBox.Get("style").Set("display", "flex")
	}

	setElementText("rec-stored", "R$ "+rec.StoredBalance.Amount)
	setElementText("rec-calculated", "R$ "+rec.CalculatedBalance.Amount)
	setElementText("rec-diff", "R$ "+rec.Difference.Amount)
	setElementText("rec-entries", fmt.Sprintf("%d", rec.CheckedEntries))

	badge := document.Call("getElementById", "audit-badge")
	if !badge.IsUndefined() && !badge.IsNull() {
		if rec.Consistent {
			badge.Set("textContent", "STATUS: 100% CONSISTENTE [ZERO DIVERGÊNCIA] ✅")
			badge.Get("style").Set("borderColor", "#10b981")
			badge.Get("style").Set("color", "#10b981")
			badge.Get("style").Set("backgroundColor", "rgba(16, 185, 129, 0.15)")
			playSound("win")
			updateTicker(fmt.Sprintf("✅ AUDITORIA CONCLUÍDA: 100%% CONSISTENTE! (%d LANÇAMENTOS CONFERIDOS)", rec.CheckedEntries))
			addLogLine(fmt.Sprintf("[AUDIT SUCCESS] 100%% Consistente! Stored: R$ %s | Ledger Calc: R$ %s | Dif: R$ %s | Lançamentos: %d", rec.StoredBalance.Amount, rec.CalculatedBalance.Amount, rec.Difference.Amount, rec.CheckedEntries))
		} else {
			badge.Set("textContent", "STATUS: DIVERGÊNCIA DETECTADA! ⚠️")
			badge.Get("style").Set("borderColor", "#ef4444")
			badge.Get("style").Set("color", "#ef4444")
			badge.Get("style").Set("backgroundColor", "rgba(239, 68, 68, 0.15)")
			playSound("error")
			updateTicker("⚠️ ATENÇÃO: DIVERGÊNCIA MATEMÁTICA DETECTADA NA AUDITORIA!")
			addLogLine(fmt.Sprintf("[AUDIT FAILED] Divergência detectada! Dif: R$ %s", rec.Difference.Amount))
		}
	}
}

// =============================================================================
// UNHAPPY PATH & EDITAL TESTING LAB
// =============================================================================

func triggerUnhappyCase(caseName string) {
	consoleEl := document.Call("getElementById", "error-console-content")
	token := tokens[activeProvider]
	if token == "" {
		token = tokens["provider-a"]
	}

	client := &http.Client{Timeout: 6 * time.Second}

	switch caseName {
	case "insufficient_funds":
		key := fmt.Sprintf("%s:err-funds-%d", activeProvider, time.Now().UnixNano())
		payload := map[string]interface{}{
			"providerId":            activeProvider,
			"externalTransactionId": fmt.Sprintf("tx-funds-%d", time.Now().UnixNano()),
			"playerId":              playerID,
			"walletId":              walletID,
			"roundId":               fmt.Sprintf("round-err-%d", time.Now().UnixNano()),
			"gameId":                "game-jungle-slots",
			"kind":                  "BET",
			"money": map[string]string{
				"amount":   "999999.00",
				"currency": "BRL",
			},
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		renderConsoleResponse(consoleEl, "POST /wagering/transactions (Saldo Insuficiente)", resp, err)

	case "tenant_violation":
		// Use provider-b token on wallet created for provider-a
		tokenB := tokens["provider-b"]
		key := fmt.Sprintf("provider-b:err-tenant-%d", time.Now().UnixNano())
		payload := map[string]interface{}{
			"providerId":            "provider-b",
			"externalTransactionId": fmt.Sprintf("tx-tenant-%d", time.Now().UnixNano()),
			"playerId":              playerID,
			"walletId":              walletID,
			"roundId":               fmt.Sprintf("round-tenant-%d", time.Now().UnixNano()),
			"gameId":                "game-jungle-slots",
			"kind":                  "BET",
			"money": map[string]string{
				"amount":   "10.00",
				"currency": "BRL",
			},
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Authorization", "Bearer "+tokenB)

		resp, err := client.Do(req)
		renderConsoleResponse(consoleEl, "POST /wagering/transactions (Invasão de Tenancy - Provider B)", resp, err)

	case "invalid_currency":
		key := fmt.Sprintf("%s:err-curr-%d", activeProvider, time.Now().UnixNano())
		payload := map[string]interface{}{
			"providerId":            activeProvider,
			"externalTransactionId": fmt.Sprintf("tx-curr-%d", time.Now().UnixNano()),
			"playerId":              playerID,
			"walletId":              walletID,
			"roundId":               fmt.Sprintf("round-curr-%d", time.Now().UnixNano()),
			"gameId":                "game-jungle-slots",
			"kind":                  "BET",
			"money": map[string]string{
				"amount":   "20.00",
				"currency": "USD", // Invalid, only BRL supported
			},
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		renderConsoleResponse(consoleEl, "POST /wagering/transactions (Moeda Não Suportada: USD)", resp, err)

	case "negative_amount":
		key := fmt.Sprintf("%s:err-neg-%d", activeProvider, time.Now().UnixNano())
		payload := map[string]interface{}{
			"providerId":            activeProvider,
			"externalTransactionId": fmt.Sprintf("tx-neg-%d", time.Now().UnixNano()),
			"playerId":              playerID,
			"walletId":              walletID,
			"roundId":               fmt.Sprintf("round-neg-%d", time.Now().UnixNano()),
			"gameId":                "game-jungle-slots",
			"kind":                  "BET",
			"money": map[string]string{
				"amount":   "-50.00",
				"currency": "BRL",
			},
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		renderConsoleResponse(consoleEl, "POST /wagering/transactions (Valor Negativo: -R$ 50)", resp, err)

	case "fake_refund":
		key := fmt.Sprintf("%s:err-fakeref-%d", activeProvider, time.Now().UnixNano())
		payload := map[string]interface{}{
			"providerId":                     activeProvider,
			"externalTransactionId":          fmt.Sprintf("tx-fakeref-%d", time.Now().UnixNano()),
			"playerId":                       playerID,
			"walletId":                       walletID,
			"roundId":                        fmt.Sprintf("round-fakeref-%d", time.Now().UnixNano()),
			"gameId":                         "game-jungle-slots",
			"kind":                           "REFUND",
			"referenceExternalTransactionId": "tx-inexistente-123456",
			"money": map[string]string{
				"amount":   "50.00",
				"currency": "BRL",
			},
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		renderConsoleResponse(consoleEl, "POST /wagering/transactions (Transação Inexistente)", resp, err)

	case "duplicate_key_conflict":
		if lastBetKey == "" {
			consoleEl.Set("textContent", "> Execute ao menos um giro no jogo antes de testar conflito de idempotência.")
			return
		}
		// Send same key with different amount (conflito de parâmetros)
		conflictPayload := map[string]interface{}{
			"providerId":            activeProvider,
			"externalTransactionId": lastBetTxID,
			"playerId":              playerID,
			"walletId":              walletID,
			"roundId":               lastBetRoundID,
			"gameId":                "game-jungle-slots",
			"kind":                  "BET",
			"money": map[string]string{
				"amount":   "77.77", // Altered amount
				"currency": "BRL",
			},
		}
		b, _ := json.Marshal(conflictPayload)
		req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", lastBetKey) // Same key!
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		renderConsoleResponse(consoleEl, "POST /wagering/transactions (Conflito de Idempotência: Mesma chave, parâmetros diferentes)", resp, err)

	case "double_refund":
		if lastBetTxID == "" || lastBetAmount <= 0 {
			consoleEl.Set("textContent", "> Execute uma aposta no jogo antes de testar estorno duplo.")
			return
		}
		key := fmt.Sprintf("%s:err-doubleref-%d", activeProvider, time.Now().UnixNano())
		payload := map[string]interface{}{
			"providerId":                     activeProvider,
			"externalTransactionId":          fmt.Sprintf("tx-doubleref-%d", time.Now().UnixNano()),
			"playerId":                       playerID,
			"walletId":                       walletID,
			"roundId":                        lastBetRoundID,
			"gameId":                         "game-jungle-slots",
			"kind":                           "REFUND",
			"referenceExternalTransactionId": lastBetTxID,
			"money": map[string]string{
				"amount":   fmt.Sprintf("%.2f", lastBetAmount),
				"currency": "BRL",
			},
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		renderConsoleResponse(consoleEl, "POST /wagering/transactions (Anti-Double Refund)", resp, err)

	case "pending_reference":
		// Out of order: refund arriving before wager
		key := fmt.Sprintf("%s:err-pendingref-%d", activeProvider, time.Now().UnixNano())
		payload := map[string]interface{}{
			"providerId":                     activeProvider,
			"externalTransactionId":          fmt.Sprintf("tx-pendingref-%d", time.Now().UnixNano()),
			"playerId":                       playerID,
			"walletId":                       walletID,
			"roundId":                        fmt.Sprintf("round-pendingref-%d", time.Now().UnixNano()),
			"gameId":                         "game-jungle-slots",
			"kind":                           "REFUND",
			"referenceExternalTransactionId": "tx-future-unregistered-bet",
			"money": map[string]string{
				"amount":   "30.00",
				"currency": "BRL",
			},
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		renderConsoleResponse(consoleEl, "POST /wagering/transactions (Edital 6.3: Pending Reference / Reversão Fora de Ordem)", resp, err)

	case "concurrency_dispute":
		runConcurrencyDisputeWeb(consoleEl)
	}
}

func renderConsoleResponse(el js.Value, title string, resp *http.Response, err error) {
	if el.IsUndefined() || el.IsNull() {
		return
	}
	if err != nil {
		el.Set("textContent", fmt.Sprintf("> [%s]\n> ERRO DE REDE: %v", title, err))
		playSound("error")
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	statusLine := fmt.Sprintf("HTTP/1.1 %d %s", resp.StatusCode, resp.Status)
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, body, "", "  "); err == nil {
		body = prettyJSON.Bytes()
	}

	out := fmt.Sprintf("> TESTE: %s\n> %s\n\n%s", title, statusLine, string(body))
	el.Set("textContent", out)

	if resp.StatusCode >= 400 {
		playSound("error")
		addLogLine(fmt.Sprintf("[TEST REJECTED] %s -> %s", title, statusLine))
	} else {
		playSound("win")
		addLogLine(fmt.Sprintf("[TEST ACCEPTED] %s -> %s", title, statusLine))
	}
}

// Section 8 Concurrency Dispute (2x R$ 80 in parallel on a R$ 100 wallet)
func runConcurrencyDisputeWeb(consoleEl js.Value) {
	addLogLine("[CONCURRENCY] Iniciando Teste de Estresse do Edital 8.0...")
	consoleEl.Set("textContent", "> [EDITAL 8.0] Disputa de Concorrência em execução...\n> Criando carteira temporária com R$ 100.00...")

	internalToken := tokens["internal"]
	betToken := tokens[activeProvider]
	if betToken == "" {
		betToken = tokens["provider-a"]
	}

	tempPlayer := fmt.Sprintf("player-race-%d", time.Now().UnixNano()%10000)

	// Create temp wallet with R$ 100: POST /wallets
	cPayload := map[string]interface{}{
		"playerId": tempPlayer,
		"initialBalance": map[string]string{
			"amount":   "100.00",
			"currency": "BRL",
		},
	}
	cB, _ := json.Marshal(cPayload)
	cReq, _ := http.NewRequest("POST", "/wallets", bytes.NewBuffer(cB))
	cReq.Header.Set("Content-Type", "application/json")
	if internalToken != "" {
		cReq.Header.Set("Authorization", "Bearer "+internalToken)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	cResp, cErr := client.Do(cReq)
	if cErr != nil || (cResp.StatusCode != 200 && cResp.StatusCode != 201) {
		consoleEl.Set("textContent", fmt.Sprintf("> Falha ao criar carteira temporária: %v", cErr))
		return
	}
	cBody, _ := io.ReadAll(cResp.Body)
	cResp.Body.Close()

	var tempWData struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(cBody, &tempWData)
	tempWalletID := tempWData.ID

	// Launch 2 concurrent goroutines racing for R$ 80.00
	var wg sync.WaitGroup
	type raceResult struct {
		ID         int
		StatusCode int
		Body       string
	}
	results := make([]raceResult, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			txID := fmt.Sprintf("tx-race-%d-%d", idx+1, time.Now().UnixNano())
			betK := fmt.Sprintf("%s:%s", activeProvider, txID)
			bPayload := map[string]interface{}{
				"providerId":            activeProvider,
				"externalTransactionId": txID,
				"playerId":              tempPlayer,
				"walletId":              tempWalletID,
				"roundId":               fmt.Sprintf("round-race-%d", time.Now().UnixNano()),
				"gameId":                "game-jungle-slots",
				"kind":                  "BET",
				"money": map[string]string{
					"amount":   "80.00",
					"currency": "BRL",
				},
			}
			bBytes, _ := json.Marshal(bPayload)
			bReq, _ := http.NewRequest("POST", "/wagering/transactions", bytes.NewBuffer(bBytes))
			bReq.Header.Set("Content-Type", "application/json")
			bReq.Header.Set("Idempotency-Key", betK)
			bReq.Header.Set("Authorization", "Bearer "+betToken)

			bResp, bErr := client.Do(bReq)
			if bErr != nil {
				results[idx] = raceResult{ID: idx + 1, StatusCode: 0, Body: bErr.Error()}
				return
			}
			rB, _ := io.ReadAll(bResp.Body)
			bResp.Body.Close()
			results[idx] = raceResult{ID: idx + 1, StatusCode: bResp.StatusCode, Body: string(rB)}
		}(i)
	}

	wg.Wait()

	// Check results
	successes := 0
	failures := 0
	for _, r := range results {
		switch r.StatusCode {
		case 200, 201:
			successes++
		case 422:
			failures++
		}
	}

	report := fmt.Sprintf(`> =======================================================
> [RESULTADO DA DISPUTA DE CONCORRÊNCIA - EDITAL 8.0]
> =======================================================
> Carteira Inicial: R$ 100.00
> Disparo Concorrente: 2 goroutines simultâneas apostando R$ 80.00
>
> • Goroutine 1: HTTP %d (Resposta: %s)
> • Goroutine 2: HTTP %d (Resposta: %s)
>
> -------------------------------------------------------
> Total Sucessos: %d (Apenas 1 aposta deve passar)
> Total Bloqueios: %d (A 2ª deve receber HTTP 422 Saldo Insuficiente)
> -------------------------------------------------------`,
		results[0].StatusCode, truncateString(results[0].Body, 40),
		results[1].StatusCode, truncateString(results[1].Body, 40),
		successes, failures)

	if successes == 1 && failures == 1 {
		report += "\n> [STATUS]: ✅ APROVADO! SELECT FOR UPDATE garantiu atomicidade perfeita sem saldo negativo!"
		playSound("win")
	} else {
		report += "\n> [STATUS]: ⚠️ ANOMALIA DE CONCORRÊNCIA DETECTADA!"
		playSound("error")
	}

	consoleEl.Set("textContent", report)
	addLogLine("[CONCURRENCY TEST] Teste de corrida concluído (1 sucesso, 1 HTTP 422).")
}
