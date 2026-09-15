# Estágio de Compilação (Build Stage)
# A versão da imagem deve acompanhar a versão mínima declarada no go.mod.
FROM golang:1.26.4-alpine3.23 AS builder

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

# Estágio final mínimo, sem distribuição Linux ou gerenciador de pacotes.
# O binário foi compilado sem CGO e pode executar diretamente no scratch.
FROM scratch

WORKDIR /app

# Copia somente a cadeia de certificados necessária para chamadas HTTPS,
# como a validação das chaves públicas do Keycloak.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Copia o binário e as migrations do estágio builder
COPY --from=builder /app/bin/api /app/api
COPY --from=builder /app/migrations /app/migrations

# Executa o processo com um UID sem privilégios. O scratch não possui usuários
# nomeados, por isso usamos diretamente o identificador numérico.
USER 65532:65532

# Expõe a porta HTTP da aplicação
EXPOSE 8000

# Executa o serviço de apostas
ENTRYPOINT ["/app/api"]
