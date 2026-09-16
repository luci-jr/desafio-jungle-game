#!/usr/bin/env bash
set -e

export PATH="/usr/local/go/bin:$PATH"

echo "🦀 [Go WebAssembly] Compilando cliente Web em Go (cmd/wasm)..."

# Copia wasm_exec.js do ambiente Go
WASM_EXEC_PATH=$(find /usr/local/go -name "wasm_exec.js" 2>/dev/null | head -n 1)
if [ -n "$WASM_EXEC_PATH" ]; then
    echo "📋 Copiando wasm_exec.js de $WASM_EXEC_PATH..."
    cp "$WASM_EXEC_PATH" ./internal/infrastructure/http/web/static/wasm_exec.js
fi

# Compila o binário game.wasm
echo "⚙️  Compilando com GOOS=js GOARCH=wasm..."
GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o ./internal/infrastructure/http/web/static/game.wasm ./cmd/wasm

echo "✅ game.wasm gerado com sucesso em internal/infrastructure/http/web/static/game.wasm ($(du -h ./internal/infrastructure/http/web/static/game.wasm | cut -f1))"
echo "🌐 Acesse a interface web em http://localhost:8000/app/"
