FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copia go.mod e go.sum primeiro para aproveitar o cache de camadas do Docker
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Compila binário estático otimizado sem símbolos de debug (-s -w)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/catalog-enricher ./cmd/api

# Etapa 2: Runtime leve e seguro
FROM alpine:3.21

# ca-certificates é obrigatório para requisições HTTPS (API do Gemini)
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/catalog-enricher .

# Roda como usuário não-root por segurança
USER nobody:nobody

EXPOSE 8080

ENTRYPOINT ["./catalog-enricher"]