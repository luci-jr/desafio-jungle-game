# Estágio de Compilação (Build Stage)
# A versão da imagem deve acompanhar a versão mínima declarada no go.mod.
FROM golang:1.26.4-alpine3.22 AS builder

WORKDIR /app

# Atualiza os pacotes da imagem base sem adicionar ferramentas desnecessárias.
RUN apk upgrade --no-cache

# Copia dependências Go para cache otimizado
COPY go.mod go.sum ./
RUN go mod download

# Copia o código-fonte
COPY . .

# Compila o binário otimizado e stripped (sem CGO)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /app/bin/api ./cmd/api

# Estágio Final Enxuto (Runtime Stage)
FROM alpine:3.22

WORKDIR /app

# Atualiza os pacotes e instala somente certificados SSL e dados de timezone.
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates tzdata

# A API não precisa de privilégios administrativos em tempo de execução.
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Copia o binário e as migrations do estágio builder
COPY --from=builder /app/bin/api /app/api
COPY --from=builder /app/migrations /app/migrations

# Executa o processo com o usuário restrito criado acima.
USER appuser

# Expõe a porta HTTP da aplicação
EXPOSE 8000

# Executa o serviço de apostas
ENTRYPOINT ["/app/api"]
