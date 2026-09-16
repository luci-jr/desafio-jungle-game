/**
 * JUNGLE SLOTS 1989 — RETRO ARCADE & AUDIT COCKPIT ENGINE
 * Vanilla JS Controller, Web Audio 8-Bit Chiptune & REST Client
 */

// =============================================================================
// STATE & CONFIG
// =============================================================================
const state = {
  tokens: {
    internal: null,
    providerA: null,
    providerB: null,
  },
  activeProvider: 'provider-a',
  playerId: null,
  walletId: null,
  currentBalance: 500.0,
  currentBet: 50.0,
  isSpinning: false,
  soundEnabled: true,
  lastBetTx: null,
  recentTransactions: [],
};

const SYMBOLS = [
  { id: 'muiraquita', char: '💎', name: 'Muiraquitã da Sorte', mult: 20, weight: 1 },
  { id: 'acai', char: '🫐', name: 'Açaí Grosso do Pará', mult: 15, weight: 2 },
  { id: 'filhote', char: '🐟', name: 'Filhote do Ver-o-Peso', mult: 8, weight: 3 },
  { id: 'manga', char: '🥭', name: 'Manga de Belém', mult: 5, weight: 4 },
  { id: 'castanha', char: '🌰', name: 'Castanha-do-Pará', mult: 3, weight: 5 },
  { id: 'jaguar', char: '🐆', name: 'Onça do Marajó', mult: 2, weight: 6 },
  { id: 'macaw', char: '🦜', name: 'Arara da Amazônia', mult: 1.5, weight: 7 },
];

// Flat weighted symbols array for random picks
const WEIGHTED_REEL = [];
SYMBOLS.forEach((s) => {
  for (let i = 0; i < s.weight; i++) WEIGHTED_REEL.push(s);
});

// =============================================================================
// 8-BIT CHIPTUNE SYNTHESIZER (WEB AUDIO API)
// =============================================================================
let audioCtx = null;

function getAudioCtx() {
  if (!audioCtx) {
    const AudioContextClass = window.AudioContext || window.webkitAudioContext;
    audioCtx = new AudioContextClass();
  }
  if (audioCtx.state === 'suspended') {
    audioCtx.resume();
  }
  return audioCtx;
}

function playTone(freq, type, duration, delay = 0, gainLevel = 0.1) {
  if (!state.soundEnabled) return;
  try {
    const ctx = getAudioCtx();
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();

    osc.type = type; // 'square', 'triangle', 'sawtooth'
    osc.frequency.setValueAtTime(freq, ctx.currentTime + delay);

    gain.gain.setValueAtTime(gainLevel, ctx.currentTime + delay);
    gain.gain.exponentialRampToValueAtTime(0.0001, ctx.currentTime + delay + duration);

    osc.connect(gain);
    gain.connect(ctx.destination);

    osc.start(ctx.currentTime + delay);
    osc.stop(ctx.currentTime + delay + duration);
  } catch (e) {
    console.warn('Audio error:', e);
  }
}

// =============================================================================
// TOUCH & HAPTIC VIBRATION ENGINE (MOBILE OPTIMIZATION)
// =============================================================================
function triggerHaptic(type = 'light') {
  if (typeof navigator !== 'undefined' && 'vibrate' in navigator) {
    try {
      if (type === 'light') navigator.vibrate(18);
      else if (type === 'medium') navigator.vibrate(35);
      else if (type === 'win') navigator.vibrate([40, 50, 80, 50, 120]);
      else if (type === 'error') navigator.vibrate([50, 50, 50]);
    } catch (e) {
      // Ignora restrições do navegador para vibração
    }
  }
}

window.scrollToSectionGo = function (sectionId) {
  triggerHaptic('light');
  const target = document.getElementById(sectionId);
  if (target) {
    const navBar = document.getElementById('mobile-section-nav');
    const navHeight = navBar ? navBar.offsetHeight : 0;
    const targetPos = target.getBoundingClientRect().top + window.pageYOffset - navHeight - 10;
    window.scrollTo({ top: targetPos, behavior: 'smooth' });

    const btnCabinet = document.getElementById('btn-nav-cabinet');
    const btnCockpit = document.getElementById('btn-nav-cockpit');
    if (btnCabinet && btnCockpit) {
      if (sectionId === 'slot-cabinet') {
        btnCabinet.classList.add('active');
        btnCockpit.classList.remove('active');
      } else {
        btnCockpit.classList.add('active');
        btnCabinet.classList.remove('active');
      }
    }
  }
};

function setupMobileNavObserver() {
  const cabinet = document.getElementById('slot-cabinet');
  const cockpit = document.getElementById('cockpit-terminal');
  const btnCabinet = document.getElementById('btn-nav-cabinet');
  const btnCockpit = document.getElementById('btn-nav-cockpit');

  if (!cabinet || !cockpit || !btnCabinet || !btnCockpit) return;

  const updateActiveNav = () => {
    const cockpitRect = cockpit.getBoundingClientRect();
    if (cockpitRect.top <= 250) {
      btnCockpit.classList.add('active');
      btnCabinet.classList.remove('active');
    } else {
      btnCabinet.classList.add('active');
      btnCockpit.classList.remove('active');
    }
  };

  window.addEventListener('scroll', updateActiveNav, { passive: true });
}

// 8-bit Sound Effects with Haptic Feedback
const SFX = {
  coin() {
    triggerHaptic('light');
    playTone(987.77, 'square', 0.08, 0, 0.12);
    playTone(1318.51, 'square', 0.28, 0.08, 0.12);
  },
  spinTick() {
    playTone(180, 'triangle', 0.03, 0, 0.08);
  },
  reelStop() {
    triggerHaptic('light');
    playTone(120, 'square', 0.08, 0, 0.15);
  },
  win() {
    triggerHaptic('win');
    playTone(523.25, 'square', 0.1, 0, 0.15); // C5
    playTone(659.25, 'square', 0.1, 0.08, 0.15); // E5
    playTone(783.99, 'square', 0.1, 0.16, 0.15); // G5
    playTone(1046.5, 'square', 0.35, 0.24, 0.18); // C6
  },
  jackpot() {
    triggerHaptic('win');
    const notes = [523.25, 659.25, 783.99, 1046.5, 783.99, 1046.5, 1318.51];
    notes.forEach((freq, idx) => {
      playTone(freq, 'square', 0.12, idx * 0.09, 0.15);
    });
  },
  error() {
    triggerHaptic('error');
    playTone(160, 'sawtooth', 0.15, 0, 0.2);
    playTone(110, 'sawtooth', 0.25, 0.12, 0.2);
  },
  buttonClick() {
    triggerHaptic('light');
    playTone(440, 'triangle', 0.04, 0, 0.08);
  },
};

// =============================================================================
// LOGGING & TERMINAL OUTPUT
// =============================================================================
function logEvent(msg, type = 'info') {
  const container = document.getElementById('log-lines');
  if (!container) return;

  const line = document.createElement('div');
  line.className = 'log-line';
  const time = new Date().toLocaleTimeString('pt-BR');
  line.innerHTML = `> <span style="color:#5d718a">[${time}]</span> ${msg}`;
  container.appendChild(line);
  container.scrollTop = container.scrollHeight;
}

// =============================================================================
// API CLIENT
// =============================================================================
async function fetchTokens() {
  try {
    const resp = await fetch('/app/api/tokens');
    if (!resp.ok) {
      throw new Error(`Falha ao obter tokens: HTTP ${resp.status}`);
    }
    const data = await resp.json();
    state.tokens.internal = data.internal;
    state.tokens.providerA = data.providerA;
    state.tokens.providerB = data.providerB;
    logEvent('Tokens JWT OIDC carregados do Keycloak com sucesso!');
  } catch (err) {
    logEvent(`Erro ao carregar tokens: ${err.message}`, 'error');
  }
}

async function checkHealth() {
  try {
    const resp = await fetch('/health/ready');
    const data = await resp.json();
    if (data.status === 'UP') {
      document.getElementById('lbl-api-status').innerText = 'ONLINE';
      document.getElementById('lbl-db-status').innerText = data.database || 'CONNECTED';
      document.getElementById('lbl-sqs-status').innerText = data.sqs || 'FIFO UP';
    }
  } catch (err) {
    document.getElementById('lbl-api-status').innerText = 'OFFLINE';
    document.getElementById('lbl-api-status').style.color = 'var(--neon-red)';
  }
}

function getActiveProviderToken() {
  if (state.activeProvider === 'provider-b') {
    return state.tokens.providerB;
  }
  return state.tokens.providerA;
}

// Cria ou inicializa uma carteira para o jogador
async function initPlayerWallet(forceNew = false) {
  let pid = localStorage.getItem('jungle_player_id');
  let wid = localStorage.getItem('jungle_wallet_id');

  // Limpa chaves corrompidas
  if (wid === 'undefined' || wid === 'null' || !wid) {
    wid = null;
  }
  if (pid === 'undefined' || pid === 'null' || !pid) {
    pid = null;
  }

  if (forceNew || !pid || !wid) {
    pid = 'player-arcade-' + Date.now();
    logEvent(`Criando nova carteira para o jogador: ${pid}...`);

    try {
      const resp = await fetch('/wallets', {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${state.tokens.internal}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          playerId: pid,
          initialBalance: {
            amount: '500.00',
            currency: 'BRL',
          },
        }),
      });

      let data;
      if (!resp.ok) {
        // Se falhar (ex: conflito 409), tenta um fallback com timestamp único
        const fallbackPid = 'player-arcade-' + Date.now() + '-' + Math.floor(Math.random() * 1000);
        const retryResp = await fetch('/wallets', {
          method: 'POST',
          headers: {
            Authorization: `Bearer ${state.tokens.internal}`,
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            playerId: fallbackPid,
            initialBalance: {
              amount: '500.00',
              currency: 'BRL',
            },
          }),
        });
        if (!retryResp.ok) {
          throw new Error(await retryResp.text());
        }
        data = await retryResp.json();
        pid = fallbackPid;
      } else {
        data = await resp.json();
      }

      wid = data.id || data.walletId;
      if (!wid) {
        throw new Error('ID de carteira ausente na resposta da API');
      }

      localStorage.setItem('jungle_player_id', pid);
      localStorage.setItem('jungle_wallet_id', wid);
      SFX.coin();
      logEvent(`Carteira ${wid} provisionada com R$ 500,00!`);
      setTicker('★ CARTEIRA PROVISIONADA! SALDO: R$ 500.00! ★');
    } catch (err) {
      logEvent(`Falha na criação de carteira: ${err.message}`, 'error');
      setTicker(`★ ERRO AO CRIAR CARTEIRA: ${err.message} ★`, true);
      return;
    }
  }

  state.playerId = pid;
  state.walletId = wid;
  document.getElementById('disp-player-id').innerText = pid;
  document.getElementById('disp-wallet-id').innerText = wid;

  await syncWalletBalance();
  await refreshLedger();
}

// Sincroniza saldo atual
async function syncWalletBalance() {
  if (!state.walletId || state.walletId === 'undefined' || !state.tokens.internal) return;
  try {
    const resp = await fetch(`/wallets/${state.walletId}`, {
      headers: {
        Authorization: `Bearer ${state.tokens.internal}`,
      },
    });
    if (resp.ok) {
      const data = await resp.json();
      state.currentBalance = parseFloat(data.balance.amount);
      updateBalanceDisplay(state.currentBalance);
    }
  } catch (err) {
    console.error('Erro ao sincronizar saldo:', err);
  }
}

function updateBalanceDisplay(amount) {
  const el = document.getElementById('disp-balance');
  el.innerText = `R$ ${amount.toFixed(2)}`;
}

// =============================================================================
// REEL ANIMATION & GAMEPLAY
// =============================================================================
function pickRandomSymbol() {
  const idx = Math.floor(Math.random() * WEIGHTED_REEL.length);
  return WEIGHTED_REEL[idx];
}

async function spinReels() {
  if (state.isSpinning) return;

  // Garante que a carteira existe e é válida antes de apostar
  if (!state.walletId || state.walletId === 'undefined' || state.walletId === 'null') {
    setTicker('★ INICIALIZANDO CARTEIRA... AGUARDE UM INSTANTE ★');
    await initPlayerWallet(true);
    if (!state.walletId || state.walletId === 'undefined') {
      setTicker('★ CLIQUE EM "NOVA CARTEIRA" PARA INICIALIZAR ★', true);
      return;
    }
  }

  // Garante tokens carregados
  if (!getActiveProviderToken()) {
    await fetchTokens();
  }

  const betAmount = state.currentBet;
  if (state.currentBalance < betAmount) {
    SFX.error();
    setTicker('★ TÁ LISO, MANINHO! SALDO INSUFICIENTE PRO AÇAÍ! ★', true);
    logEvent(`Tentativa de aposta bloqueada: Saldo R$ ${state.currentBalance.toFixed(2)} < Aposta R$ ${betAmount.toFixed(2)}`, 'error');
    return;
  }

  state.isSpinning = true;
  document.getElementById('btn-spin').disabled = true;
  document.getElementById('btn-replay').disabled = true;
  document.getElementById('btn-refund').disabled = true;

  setTicker('★ TÁ RODANDO O CARIMBÓ! SEGURA O RETORNO! ★');

  // 1. Gera parâmetros únicos para a aposta
  const timestamp = Date.now();
  const externalTxId = `tx-bet-${timestamp}`;
  const idempotencyKey = `${state.activeProvider}:${externalTxId}`;
  const roundId = `round-slots-${timestamp}`;

  const betPayload = {
    providerId: state.activeProvider,
    externalTransactionId: externalTxId,
    playerId: state.playerId,
    walletId: state.walletId,
    roundId: roundId,
    gameId: 'game-jungle-slots',
    kind: 'BET',
    money: {
      amount: betAmount.toFixed(2),
      currency: 'BRL',
    },
  };

  // Armazena a transação para testes de replay e refund
  state.lastBetTx = {
    idempotencyKey,
    payload: betPayload,
    amount: betAmount.toFixed(2),
    roundId,
  };

  // 2. Dispara a aposta (BET) contra a API
  const startTime = performance.now();
  try {
    const resp = await fetch('/wagering/transactions', {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${getActiveProviderToken()}`,
        'Content-Type': 'application/json',
        'Idempotency-Key': idempotencyKey,
      },
      body: JSON.stringify(betPayload),
    });

    const elapsed = Math.round(performance.now() - startTime);

    if (!resp.ok) {
      const errData = await resp.json().catch(() => ({ error: 'Erro desconhecido' }));
      throw new Error(`[HTTP ${resp.status}] ${errData.error || errData.code || 'Falha na transação'}`);
    }

    const betResult = await resp.json();
    state.currentBalance = parseFloat(betResult.balance.amount);
    updateBalanceDisplay(state.currentBalance);

    logEvent(`[BET DEBITADO] -R$ ${betAmount.toFixed(2)} em ${elapsed}ms. Saldo: R$ ${state.currentBalance.toFixed(2)}`);

    // Inicia rotação física dos rolos
    await animateReelsSpin();

    // Determina o resultado
    const sym1 = pickRandomSymbol();
    const sym2 = pickRandomSymbol();
    const sym3 = pickRandomSymbol();

    // Atualiza símbolos na tela com animação de parada
    document.getElementById('strip-1').innerHTML = `<div class="reel-cell">${sym1.char}</div>`;
    SFX.reelStop();
    await wait(180);
    document.getElementById('strip-2').innerHTML = `<div class="reel-cell">${sym2.char}</div>`;
    SFX.reelStop();
    await wait(180);
    document.getElementById('strip-3').innerHTML = `<div class="reel-cell">${sym3.char}</div>`;
    SFX.reelStop();

    // 3. Avalia combinação vencedora
    let prizeMult = 0;
    if (sym1.id === sym2.id && sym2.id === sym3.id) {
      prizeMult = sym1.mult; // 3 iguais
    } else if (sym1.id === sym2.id || sym2.id === sym3.id || sym1.id === sym3.id) {
      prizeMult = 1.1; // 2 iguais
    }

    if (prizeMult > 0) {
      const winAmount = (betAmount * prizeMult).toFixed(2);
      await processWinTransaction(roundId, winAmount, prizeMult >= 5);
    } else {
      document.getElementById('disp-last-win').innerText = 'R$ 0.00';
      const missPhrases = [
        '★ NÃO DEU NADA, MANINHO! MAS TE ACALMA QUE JÁ VEM O FILHOTE! ★',
        '★ BOR\'ALI TOMAR UM AÇAÍ NO VER-O-PESO ENQUANTO GIRA DE NOVO! ★',
        '★ CHUVA DAS DUAS DA TARDE PASSANDO... TENTA DE NOVO! ★',
        '★ QUASE, MANO! MAIS UMA RODADA PRA SOLTAR O CARIMBÓ! ★',
      ];
      const phrase = missPhrases[Math.floor(Math.random() * missPhrases.length)];
      setTicker(phrase);
    }
  } catch (err) {
    SFX.error();
    setTicker(`★ ERRO: ${err.message} ★`, true);
    logEvent(`Erro ao executar aposta: ${err.message}`, 'error');
  } finally {
    state.isSpinning = false;
    document.getElementById('btn-spin').disabled = false;
    document.getElementById('btn-replay').disabled = false;
    
    // Atualiza botão de estorno para a nova aposta ativa
    const refundBtn = document.getElementById('btn-refund');
    refundBtn.disabled = false;
    refundBtn.querySelector('.btn-top').innerText = '↩️ ESTORNAR APOSTA';
    refundBtn.title = 'Estorna integralmente a última aposta processada (kind: REFUND) conforme os critérios do Edital.';
    if (state.lastBetTx) {
      state.lastBetTx.isRefunded = false;
    }
    
    await refreshLedger();
  }
}

// Animação dos rolos com efeito blur e sons de clique mecânico
async function animateReelsSpin() {
  const strips = [document.getElementById('strip-1'), document.getElementById('strip-2'), document.getElementById('strip-3')];
  strips.forEach((s) => s.classList.add('spinning'));

  const interval = setInterval(() => {
    SFX.spinTick();
  }, 100);

  await wait(800);
  clearInterval(interval);
  strips.forEach((s) => s.classList.remove('spinning'));
}

// Dispara transação de crédito (WIN) na mesma rodada (roundId)
async function processWinTransaction(roundId, winAmount, isJackpot) {
  setTicker(`★ JACKPOT! CREDITANDO PRÊMIO DE R$ ${winAmount}... ★`, true);
  const winTxId = `tx-win-${Date.now()}`;
  const idempotencyKey = `${state.activeProvider}:${winTxId}`;

  const winPayload = {
    providerId: state.activeProvider,
    externalTransactionId: winTxId,
    playerId: state.playerId,
    walletId: state.walletId,
    roundId: roundId,
    gameId: 'game-jungle-slots',
    kind: 'WIN',
    money: {
      amount: winAmount,
      currency: 'BRL',
    },
  };

  const startTime = performance.now();
  try {
    const resp = await fetch('/wagering/transactions', {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${getActiveProviderToken()}`,
        'Content-Type': 'application/json',
        'Idempotency-Key': idempotencyKey,
      },
      body: JSON.stringify(winPayload),
    });

    const elapsed = Math.round(performance.now() - startTime);

    if (!resp.ok) {
      const err = await resp.json();
      throw new Error(err.error || 'Falha ao creditar prêmio');
    }

    const data = await resp.json();
    state.currentBalance = parseFloat(data.balance.amount);
    updateBalanceDisplay(state.currentBalance);

    document.getElementById('disp-last-win').innerText = `R$ ${winAmount}`;

    if (isJackpot) {
      SFX.jackpot();
      setTicker(`★ ★ ★ ÉGUA DO JACKPOT! PAI D'ÉGUA, TE BANCA! R$ ${winAmount}! ★ ★ ★`, true);
    } else {
      SFX.win();
      setTicker(`★ ÉGUA, MANO! PRÊMIO DE R$ ${winAmount} CREDITADO! PAI D'ÉGUA! ★`, true);
    }

    logEvent(`[WIN CREDITADO] +R$ ${winAmount} em ${elapsed}ms! Saldo: R$ ${state.currentBalance.toFixed(2)}`);
  } catch (err) {
    logEvent(`Erro ao creditar prêmio: ${err.message}`, 'error');
  }
}

// =============================================================================
// IDEMPOTENCY REPLAY & REFUND TESTS
// =============================================================================
async function testIdempotentReplay() {
  if (!state.lastBetTx) return;

  logEvent(`[REPLAY] Re-enviando transação com Idempotency-Key: ${state.lastBetTx.idempotencyKey}...`);
  setTicker('★ TESTANDO REPLAY IDEMPOTENTE... ★');

  const startTime = performance.now();
  try {
    const resp = await fetch('/wagering/transactions', {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${getActiveProviderToken()}`,
        'Content-Type': 'application/json',
        'Idempotency-Key': state.lastBetTx.idempotencyKey,
      },
      body: JSON.stringify(state.lastBetTx.payload),
    });

    const elapsed = Math.round(performance.now() - startTime);
    const data = await resp.json();

    if (resp.ok && data.idempotentReplay === true) {
      SFX.coin();
      setTicker('★ SUCESSO: REPLAY IDEMPOTENTE! SALDO NÃO FOI DUPLICADO! ★', true);
      logEvent(`[REPLAY OK] Status: PROCESSED | idempotentReplay: TRUE | Saldo inalterado: R$ ${data.balance.amount} (${elapsed}ms)`);
    } else {
      logEvent(`Resposta inesperada no replay: ${JSON.stringify(data)}`, 'error');
    }
  } catch (err) {
    logEvent(`Erro no teste de replay: ${err.message}`, 'error');
  }
}

async function testRefund() {
  if (!state.lastBetTx) {
    alert('Nenhuma aposta realizada nesta sessão para estornar.');
    return;
  }

  if (state.lastBetTx.isRefunded) {
    alert('⚠️ Esta aposta já foi estornada! Conforme a Seção 7 do Edital, reversões duplicadas da mesma aposta são estritamente bloqueadas (Anti-Double Refund).');
    return;
  }

  const refTxId = state.lastBetTx.payload.externalTransactionId;
  const refundTxId = `tx-refund-${Date.now()}`;
  const idempotencyKey = `${state.activeProvider}:${refundTxId}`;
  const refundAmount = state.lastBetTx.amount;

  const confirmMsg = `📋 PROTOCOLO DE ESTORNO REGULAMENTAR (REFUND - EDITAL):\n\n` +
    `• Transação Referenciada (BET): ${refTxId}\n` +
    `• Provedor Autorizado: ${state.activeProvider}\n` +
    `• Jogador: ${state.playerId}\n` +
    `• Carteira: ${state.walletId}\n` +
    `• Rodada: ${state.lastBetTx.roundId}\n` +
    `• Valor a Devolver (100% integral): R$ ${refundAmount} BRL\n\n` +
    `Desejas emitir a transação de estorno?`;

  if (!confirm(confirmMsg)) return;

  logEvent(`[REFUND] Solicitando estorno regulamentar de R$ ${refundAmount} referente à tx ${refTxId}...`);
  setTicker('★ PROCESSANDO ESTORNO REGULAMENTAR DA APOSTA... ★');

  const payload = {
    providerId: state.activeProvider,
    externalTransactionId: refundTxId,
    referenceExternalTransactionId: refTxId,
    playerId: state.playerId,
    walletId: state.walletId,
    roundId: state.lastBetTx.roundId,
    gameId: 'game-jungle-slots',
    kind: 'REFUND',
    money: {
      amount: refundAmount,
      currency: 'BRL',
    },
  };

  try {
    const resp = await fetch('/wagering/transactions', {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${getActiveProviderToken()}`,
        'Content-Type': 'application/json',
        'Idempotency-Key': idempotencyKey,
      },
      body: JSON.stringify(payload),
    });

    const data = await resp.json();
    if (resp.ok) {
      SFX.coin();
      state.lastBetTx.isRefunded = true;
      state.currentBalance = parseFloat(data.balance.amount);
      updateBalanceDisplay(state.currentBalance);

      const refundBtn = document.getElementById('btn-refund');
      refundBtn.disabled = true;
      refundBtn.querySelector('.btn-top').innerText = '🚫 APOSTA JÁ ESTORNADA';
      refundBtn.title = 'Aposta revertida com sucesso. Reversões adicionais bloqueadas pelo Edital (Anti-Double Refund).';

      setTicker(`★ ESTORNO REALIZADO! +R$ ${refundAmount} DE VOLTA À CARTEIRA! ★`, true);
      logEvent(`[REFUND CONCLUÍDO] +R$ ${refundAmount} estornado com sucesso! Saldo: R$ ${state.currentBalance.toFixed(2)}`);
      await refreshLedger();
    } else {
      throw new Error(data.error || data.message || 'Falha no estorno');
    }
  } catch (err) {
    SFX.error();
    setTicker(`★ FALHA NO ESTORNO: ${err.message} ★`, true);
    logEvent(`Erro ao solicitar estorno: ${err.message}`, 'error');
  }
}

// =============================================================================
// LEDGER & RECONCILIATION
// =============================================================================
async function refreshLedger() {
  if (!state.walletId || !state.tokens.internal) return;

  try {
    const resp = await fetch(`/wallets/${state.walletId}/ledger`, {
      headers: {
        Authorization: `Bearer ${state.tokens.internal}`,
      },
    });

    if (!resp.ok) return;
    const data = await resp.json();
    const entries = Array.isArray(data) ? data : (data.entries || []);
    state.recentTransactions = entries;

    const tbody = document.getElementById('ledger-tbody');
    if (!entries || entries.length === 0) {
      tbody.innerHTML = '<tr><td colspan="7" class="terminal-dim">> Nenhum lançamento registrado no Livro do Ver-o-Peso. Inicie um giro!</td></tr>';
      return;
    }

    tbody.innerHTML = entries
      .map((e, idx) => {
        const kind = e.kind || (e.direction === 'DEBIT' ? 'BET' : 'WIN');
        const isBet = kind === 'BET';
        const isWin = kind === 'WIN';
        const isRefund = kind === 'REFUND';
        const isRollback = kind === 'ROLLBACK';
        const isOpening = kind === 'OPENING';

        let badgeClass = 'badge-bet';
        if (isWin) badgeClass = 'badge-win';
        else if (isRefund) badgeClass = 'badge-refund';
        else if (isRollback) badgeClass = 'badge-rollback';
        else if (isOpening) badgeClass = 'badge-opening';

        const sign = e.direction === 'DEBIT' ? '-' : '+';
        const refDisplay = e.referenceExternalTransactionId
          ? `<span class="badge-ref" title="Referência Externa: ${e.referenceExternalTransactionId}">Ref: ${e.referenceExternalTransactionId.slice(0, 14)}...</span>`
          : '<span style="color:#64748b">---</span>';

        const extDisplay = e.externalTransactionId
          ? `<span title="${e.externalTransactionId}">${e.externalTransactionId.slice(0, 16)}...</span>`
          : 'Abertura';

        return `
          <tr>
            <td>#${entries.length - idx}</td>
            <td><span class="badge-kind ${badgeClass}">${kind}</span></td>
            <td><strong style="color:${e.direction === 'DEBIT' ? '#f87171' : '#4ade80'}">${sign}R$ ${parseFloat(e.amount).toFixed(2)}</strong></td>
            <td>R$ ${parseFloat(e.balanceAfter).toFixed(2)}</td>
            <td style="font-size:0.75rem; color:var(--term-dim)">${extDisplay}</td>
            <td style="font-size:0.75rem;">${refDisplay}</td>
            <td><button class="btn-inspect" onclick="inspectTransaction(${idx})">INSPECIONAR</button></td>
          </tr>
        `;
      })
      .join('');
  } catch (err) {
    console.error('Falha ao atualizar ledger:', err);
  }
}

async function runMathematicalReconciliation() {
  if (!state.walletId || !state.tokens.internal) return;

  logEvent(`[AUDIT] Disparando auditoria matemática para a carteira ${state.walletId}...`);
  const btn = document.getElementById('btn-run-reconciliation');
  btn.innerText = '⚡ AUDITANDO BASE DE DADOS...';
  btn.disabled = true;

  try {
    const resp = await fetch(`/wallets/${state.walletId}/reconciliation`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${state.tokens.internal}`,
      },
    });

    const data = await resp.json();
    document.getElementById('reconcile-result').style.display = 'flex';
    document.getElementById('rec-stored').innerText = `R$ ${parseFloat(data.storedBalance.amount).toFixed(2)}`;
    document.getElementById('rec-calculated').innerText = `R$ ${parseFloat(data.calculatedBalance.amount).toFixed(2)}`;
    document.getElementById('rec-diff').innerText = `R$ ${parseFloat(data.difference.amount).toFixed(2)}`;
    document.getElementById('rec-entries').innerText = data.checkedEntries;

    const badge = document.getElementById('audit-badge');
    if (data.consistent) {
      SFX.coin();
      badge.innerText = 'STATUS: CONSISTENTE [ZERO DIVERGÊNCIA] ✅';
      badge.style.borderColor = 'var(--neon-green)';
      badge.style.color = 'var(--neon-green)';
      logEvent(`[AUDIT PASSED] 100% Consistente! ${data.checkedEntries} lançamentos verificados. Diferença: R$ ${data.difference.amount}`);
    } else {
      SFX.error();
      badge.innerText = 'STATUS: DIVERGÊNCIA MATEMÁTICA DETECTADA! ⚠️';
      badge.style.borderColor = 'var(--neon-red)';
      badge.style.color = 'var(--neon-red)';
      logEvent(`[AUDIT FAILED] Divergência detectada! Diff: R$ ${data.difference.amount}`, 'error');
    }
  } catch (err) {
    logEvent(`Erro ao reconciliar: ${err.message}`, 'error');
  } finally {
    btn.innerText = '⚡ EXECUTAR AUDITORIA MATEMÁTICA AGORA';
    btn.disabled = false;
  }
}

// =============================================================================
// UNHAPPY PATH TEST LAB
// =============================================================================
async function runUnhappyPathTest(testCase) {
  const consoleOut = document.getElementById('error-console-content');
  consoleOut.innerText = `> Executando cenário de teste: ${testCase}...`;

  let endpoint = '/wagering/transactions';
  let method = 'POST';
  let headers = {
    Authorization: `Bearer ${getActiveProviderToken()}`,
    'Content-Type': 'application/json',
    'Idempotency-Key': `test-edge-${Date.now()}`,
  };
  let body = {};

  switch (testCase) {
    case 'insufficient_funds':
      body = {
        providerId: state.activeProvider,
        externalTransactionId: `tx-fail-${Date.now()}`,
        playerId: state.playerId,
        walletId: state.walletId,
        roundId: `round-fail-${Date.now()}`,
        gameId: 'game-jungle-slots',
        kind: 'BET',
        money: { amount: '999999.00', currency: 'BRL' },
      };
      break;

    case 'tenant_violation':
      // Envia token do provider-b mas informa providerId: "provider-a"
      headers.Authorization = `Bearer ${state.tokens.providerB}`;
      body = {
        providerId: 'provider-a',
        externalTransactionId: `tx-invader-${Date.now()}`,
        playerId: state.playerId,
        walletId: state.walletId,
        roundId: `round-invader-${Date.now()}`,
        gameId: 'game-jungle-slots',
        kind: 'BET',
        money: { amount: '10.00', currency: 'BRL' },
      };
      break;

    case 'invalid_currency':
      body = {
        providerId: state.activeProvider,
        externalTransactionId: `tx-cur-${Date.now()}`,
        playerId: state.playerId,
        walletId: state.walletId,
        roundId: `round-cur-${Date.now()}`,
        gameId: 'game-jungle-slots',
        kind: 'BET',
        money: { amount: '10.00', currency: 'USD' },
      };
      break;

    case 'negative_amount':
      body = {
        providerId: state.activeProvider,
        externalTransactionId: `tx-neg-${Date.now()}`,
        playerId: state.playerId,
        walletId: state.walletId,
        roundId: `round-neg-${Date.now()}`,
        gameId: 'game-jungle-slots',
        kind: 'BET',
        money: { amount: '-50.00', currency: 'BRL' },
      };
      break;

    case 'fake_refund':
      body = {
        providerId: state.activeProvider,
        externalTransactionId: `tx-fake-${Date.now()}`,
        referenceExternalTransactionId: 'tx-inexistente-999999',
        playerId: state.playerId,
        walletId: state.walletId,
        roundId: `round-fake-${Date.now()}`,
        gameId: 'game-jungle-slots',
        kind: 'REFUND',
        money: { amount: '50.00', currency: 'BRL' },
      };
      break;

    case 'duplicate_key_conflict':
      if (!state.lastBetTx) {
        consoleOut.innerText = '> Execute pelo menos uma aposta primeiro para obter uma Idempotency-Key real!';
        return;
      }
      headers['Idempotency-Key'] = state.lastBetTx.idempotencyKey;
      body = { ...state.lastBetTx.payload, money: { amount: '888.00', currency: 'BRL' } };
      break;

    case 'double_refund':
      if (!state.lastBetTx) {
        consoleOut.innerText = '> Execute pelo menos uma aposta primeiro para obter uma referência!';
        return;
      }
      headers['Idempotency-Key'] = `${state.activeProvider}:tx-double-refund-${Date.now()}`;
      body = {
        providerId: state.activeProvider,
        externalTransactionId: `tx-double-refund-${Date.now()}`,
        referenceExternalTransactionId: state.lastBetTx.payload.externalTransactionId,
        playerId: state.playerId,
        walletId: state.walletId,
        roundId: state.lastBetTx.roundId,
        gameId: 'game-jungle-slots',
        kind: 'REFUND',
        money: { amount: state.lastBetTx.amount, currency: 'BRL' },
      };
      break;

    case 'pending_reference':
      headers['Idempotency-Key'] = `${state.activeProvider}:tx-ref-previa-${Date.now()}`;
      body = {
        providerId: state.activeProvider,
        externalTransactionId: `tx-refund-antecipado-${Date.now()}`,
        referenceExternalTransactionId: `tx-bet-inexistente-ou-atrasada-${Date.now()}`,
        playerId: state.playerId,
        walletId: state.walletId,
        roundId: `round-antecipada-${Date.now()}`,
        gameId: 'game-jungle-slots',
        kind: 'REFUND',
        money: { amount: '50.00', currency: 'BRL' },
      };
      break;

    case 'concurrency_dispute':
      consoleOut.innerText = '> [EDITAL SEÇÃO 8] EXECUTANDO TESTE DE CONCORRÊNCIA ATÔMICA...\n' +
        '> 1. Provisionando carteira isolada com saldo de R$ 100.00 BRL...\n';
      const cPlayer = `player-concurrency-${Date.now()}`;
      try {
        const cWalletResp = await fetch('/wallets', {
          method: 'POST',
          headers: {
            Authorization: `Bearer ${state.tokens.internal}`,
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            playerId: cPlayer,
            initialBalance: { amount: '100.00', currency: 'BRL' },
          }),
        });
        if (!cWalletResp.ok) throw new Error(await cWalletResp.text());
        const cWalletData = await cWalletResp.json();
        const cWalletId = cWalletData.id || cWalletData.walletId;

        consoleOut.innerText += `> Carteira ${cWalletId} provisionada com R$ 100.00!\n> 2. Disparando 2 apostas simultâneas de R$ 80.00 em paralelo (Promise.all)...\n`;

        const p1 = {
          providerId: state.activeProvider,
          externalTransactionId: `tx-conc-1-${Date.now()}`,
          playerId: cPlayer,
          walletId: cWalletId,
          roundId: `round-c1-${Date.now()}`,
          gameId: 'game-jungle-slots',
          kind: 'BET',
          money: { amount: '80.00', currency: 'BRL' },
        };
        const p2 = {
          providerId: state.activeProvider,
          externalTransactionId: `tx-conc-2-${Date.now()}`,
          playerId: cPlayer,
          walletId: cWalletId,
          roundId: `round-c2-${Date.now()}`,
          gameId: 'game-jungle-slots',
          kind: 'BET',
          money: { amount: '80.00', currency: 'BRL' },
        };

        const [r1, r2] = await Promise.all([
          fetch('/wagering/transactions', {
            method: 'POST',
            headers: {
              Authorization: `Bearer ${getActiveProviderToken()}`,
              'Content-Type': 'application/json',
              'Idempotency-Key': `${state.activeProvider}:${p1.externalTransactionId}`,
            },
            body: JSON.stringify(p1),
          }),
          fetch('/wagering/transactions', {
            method: 'POST',
            headers: {
              Authorization: `Bearer ${getActiveProviderToken()}`,
              'Content-Type': 'application/json',
              'Idempotency-Key': `${state.activeProvider}:${p2.externalTransactionId}`,
            },
            body: JSON.stringify(p2),
          }),
        ]);

        const fwResp = await fetch(`/wallets/${cWalletId}`, {
          headers: { Authorization: `Bearer ${state.tokens.internal}` },
        });
        const fwData = await fwResp.json();

        const successCount = (r1.status === 200 ? 1 : 0) + (r2.status === 200 ? 1 : 0);
        const rejectCount = (r1.status === 422 ? 1 : 0) + (r2.status === 422 ? 1 : 0);
        const isOk = successCount === 1 && rejectCount === 1 && fwData.balance.amount === '20.00';

        consoleOut.innerText += `\n======================================================\n` +
          `AUDITORIA DE CONCORRÊNCIA DO EDITAL:\n` +
          `• Aposta 1: HTTP ${r1.status} (${r1.status === 200 ? 'PROCESSADA ✅' : 'REJEITADA (422) ⚠️'})\n` +
          `• Aposta 2: HTTP ${r2.status} (${r2.status === 200 ? 'PROCESSADA ✅' : 'REJEITADA (422) ⚠️'})\n` +
          `• Saldo Final da Carteira: R$ ${fwData.balance.amount} BRL (Esperado exato: 20.00)\n` +
          `• Status do Edital Seção 8: ${isOk ? '100% CONFORME (PASSED) ✅' : 'DIVERGÊNCIA ❌'}\n` +
          `======================================================\n`;

        if (isOk) {
          SFX.win();
          logEvent(`[CONCORRÊNCIA EDITAL] Teste 100% APROVADO! Saldo final R$ 20.00 com 1 rejeição por saldo insuficiente.`);
        }
      } catch (err) {
        consoleOut.innerText += `\nFALHA NO TESTE: ${err.message}`;
      }
      return;
  }

  const startTime = performance.now();
  try {
    const resp = await fetch(endpoint, {
      method,
      headers,
      body: JSON.stringify(body),
    });

    const elapsed = Math.round(performance.now() - startTime);
    const respText = await resp.text();
    let parsedResp = respText;
    try {
      parsedResp = JSON.stringify(JSON.parse(respText), null, 2);
    } catch (e) {}

    SFX.error();
    consoleOut.innerText = `HTTP STATUS: ${resp.status} ${resp.statusText} (${elapsed}ms)\n\n${parsedResp}`;
    logEvent(`[TESTE BORDA] ${testCase} -> HTTP ${resp.status} (${elapsed}ms)`);
  } catch (err) {
    consoleOut.innerText = `FALHA DE REDE: ${err.message}`;
  }
}

// =============================================================================
// MODAL INSPECTOR
// =============================================================================
window.inspectTransaction = function (index) {
  const entry = state.recentTransactions[index];
  if (!entry) return;

  const modal = document.getElementById('payload-modal');
  const jsonBox = document.getElementById('modal-json-content');
  const curlBox = document.getElementById('modal-curl-content');

  jsonBox.innerText = JSON.stringify(entry, null, 2);

  const curlCmd = `curl -s -X POST http://localhost:8000/wagering/transactions \\
  -H "Authorization: Bearer <TOKEN_PROV_A>" \\
  -H "Content-Type: application/json" \\
  -H "Idempotency-Key: ${entry.externalTransactionId}" \\
  -d '${JSON.stringify(
    {
      providerId: state.activeProvider,
      externalTransactionId: entry.externalTransactionId,
      playerId: state.playerId,
      walletId: state.walletId,
      roundId: entry.roundId || 'round-slots-01',
      kind: entry.kind,
      money: { amount: entry.amount, currency: 'BRL' },
    },
    null,
    2
  )}'`;

  curlBox.innerText = curlCmd;
  modal.style.display = 'flex';
};

// =============================================================================
// UI HELPERS & LISTENERS
// =============================================================================
function setTicker(msg, isWin = false) {
  const el = document.getElementById('ticker-text');
  el.innerText = msg;
  if (isWin) {
    el.classList.add('win');
  } else {
    el.classList.remove('win');
  }
}

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// Bind Events on DOM Ready
document.addEventListener('DOMContentLoaded', async () => {
  // Toggle Sound
  document.getElementById('btn-toggle-sound').addEventListener('click', () => {
    state.soundEnabled = !state.soundEnabled;
    document.getElementById('btn-toggle-sound').innerText = state.soundEnabled ? '🔊 SFX: ON' : '🔇 SFX: OFF';
  });

  // Toggle CRT (se presente)
  const crtBtn = document.getElementById('btn-toggle-crt');
  if (crtBtn) {
    crtBtn.addEventListener('click', () => {
      document.body.classList.toggle('crt-enabled');
      const enabled = document.body.classList.contains('crt-enabled');
      crtBtn.innerText = enabled ? '📺 CRT: ON' : '📺 CRT: OFF';
    });
  }

  // Bet Chips
  document.querySelectorAll('.chip-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.chip-btn').forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');
      const val = parseFloat(btn.dataset.val);
      state.currentBet = val;
      document.getElementById('input-custom-bet').value = val.toFixed(2);
      document.getElementById('disp-current-bet').innerText = `R$ ${val.toFixed(2)}`;
      SFX.buttonClick();
    });
  });

  // Custom Bet Input
  document.getElementById('input-custom-bet').addEventListener('input', (e) => {
    const val = parseFloat(e.target.value) || 1.0;
    state.currentBet = val;
    document.getElementById('disp-current-bet').innerText = `R$ ${val.toFixed(2)}`;
  });

  // Spin Button
  document.getElementById('btn-spin').addEventListener('click', spinReels);

  // Idempotent Replay Button
  document.getElementById('btn-replay').addEventListener('click', testIdempotentReplay);

  // Refund Button
  document.getElementById('btn-refund').addEventListener('click', testRefund);

  // Função para Zerar Tudo e Recarregar o Jogo
  function triggerGameReset(forcePrompt = true) {
    if (forcePrompt) {
      const ok = confirm('⚠️ ÉGUA, MANO! QUERES REINICIAR O JOGO E ZERAR TUDO?\n\n• Uma nova carteira com R$ 500,00 será criada (garante o açaí com filhote!).\n• O histórico de apostas e auditoria do Ver-o-Peso serão zerados.\n• Os rolos e prêmios voltarão ao estado inicial.');
      if (!ok) return;
    }

    try {
      SFX.coin();
    } catch (e) {}

    localStorage.removeItem('jungle_player_id');
    localStorage.removeItem('jungle_wallet_id');
    localStorage.removeItem('jungle_recent_txs');

    setTicker('⚡ REINICIANDO O FLIPERAMA DO VER-O-PESO... ZERANDO TUDO... ⚡');

    setTimeout(() => {
      window.location.reload();
    }, 200);
  }

  // Reload / Reset Buttons
  const reloadBtn = document.getElementById('btn-reload-game');
  if (reloadBtn) {
    reloadBtn.addEventListener('click', () => triggerGameReset(true));
  }

  const resetAllBtn = document.getElementById('btn-reset-all');
  if (resetAllBtn) {
    resetAllBtn.addEventListener('click', () => triggerGameReset(true));
  }

  // New Wallet Button
  document.getElementById('btn-new-wallet').addEventListener('click', async () => {
    localStorage.removeItem('jungle_player_id');
    localStorage.removeItem('jungle_wallet_id');
    await initPlayerWallet(true);
  });

  // Provider Select
  document.getElementById('sel-provider').addEventListener('change', (e) => {
    state.activeProvider = e.target.value;
    logEvent(`Provedor ativo alterado para: ${state.activeProvider}`);
  });

  // Terminal Tabs
  document.querySelectorAll('.tab-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.tab-btn').forEach((b) => b.classList.remove('active'));
      document.querySelectorAll('.tab-content').forEach((c) => c.classList.remove('active'));
      btn.classList.add('active');
      document.getElementById(btn.dataset.tab).classList.add('active');
      SFX.buttonClick();
    });
  });

  // Refresh Ledger
  document.getElementById('btn-refresh-ledger').addEventListener('click', () => {
    refreshLedger();
    SFX.buttonClick();
  });

  // Run Mathematical Reconciliation
  document.getElementById('btn-run-reconciliation').addEventListener('click', runMathematicalReconciliation);

  // Unhappy Path Buttons
  document.querySelectorAll('.btn-error-test').forEach((btn) => {
    btn.addEventListener('click', () => {
      runUnhappyPathTest(btn.dataset.case);
    });
  });

  // Close Modal
  document.getElementById('btn-close-modal').addEventListener('click', () => {
    document.getElementById('payload-modal').style.display = 'none';
  });

  // Initial Boot
  setupMobileNavObserver();
  await fetchTokens();
  await checkHealth();
  await initPlayerWallet(false);
});
