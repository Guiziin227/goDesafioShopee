# Catalog Enricher Service

Microsserviço assíncrono de alta performance desenvolvido em **Go (Golang)** para ingestão, categorização e enriquecimento semântico de catálogos de e-commerce utilizando **Inteligência Artificial**.

Construído com foco em concorrência segura, resiliência contra falhas de rede (*rate limits*, latência e timeouts) e arquitetura desacoplada.

---

## Arquitetura e Fluxo de Dados

O serviço utiliza o padrão **Asynchronous Request-Reply** aliado a um **Bounded Worker Pool** para desacoplar a ingestão HTTP do processamento pesado de IA.

```text
[Client]
   │
   ├── 1. POST /v1/products/batch ──► [HTTP Handler] ──► [Batch Store (RWMutex)]
   │      (202 Accepted + batch_id)          │
   │                                         ▼
   │                                  [Buffered Channel]
   │                                         │
   │                                         ▼
   │                             [Worker Pool (5 Goroutines)]
   │                                         │
   │                         ┌───────────────┴───────────────┐
   │                         ▼                               ▼
   │                [AI Client (Timeout 2s)]        [Exponential Backoff]
   │                         │                       (Max 3 Retries)
   │                         └───────────────┬───────────────┘
   │                                         ▼
   └── 2. GET /v1/products/batch/{id} ◄── [Update Batch Store]
          (200 OK + Status & Results)
```

### Estrutura de Diretórios (Clean Architecture)

```text
├── cmd/
│   └── api/
│       └── main.go              # Entrypoint, injeção de dependências e Graceful Shutdown
├── internal/
│   ├── domain/
│   │   ├── batch.go             # Entidades de negócio, status e regras de domínio
│   │   └── errors.go            # Erros tipados da aplicação
│   ├── usecase/
│   │   ├── enricher.go          # Lógica de orquestração e worker pool
│   │   └── enricher_test.go     # Testes unitários e de concorrência
│   ├── infrastructure/
│   │   ├── ai/
│   │   │   └── mock_client.go   # Simulador de LLM com latência e falhas intermitentes
│   │   └── repository/
│   │       └── memory_store.go  # Armazenamento thread-safe em memória (sync.RWMutex)
│   └── delivery/
│       └── http/
│           ├── handler.go       # Endpoints REST e validação de payload
│           └── middleware.go    # Logging estruturado e recovery
├── Dockerfile                   # Build multi-stage otimizado (distroless/alpine)
├── docker-compose.yml           # Orquestração local
├── Makefile                     # Automação de tarefas de desenvolvimento
└── README.md
```

---

## Como Executar

### Pré-requisitos
* **Go 1.23+** (para execução local)
* **Docker & Docker Compose** (para execução containerizada)

### Rodando via Docker (Recomendado)
```bash
docker-compose up --build
```
O servidor estará disponível em `http://localhost:8080`.

### Rodando Localmente via Makefile
```bash
# Subir a aplicação
make run

# Executar todos os testes com detector de race conditions
make test

# Gerar binário estático otimizado
make build
```

---

## Referência da API

### 1. Submeter Lote de Produtos
Inicia o processamento assíncrono de até 50 produtos simultaneamente.

* **URL:** `POST /v1/products/batch`
* **Exemplo de Requisição:**
```bash
curl -X POST http://localhost:8080/v1/products/batch \
  -H "Content-Type: application/json" \
  -d '{
    "products": [
      {
        "id": "SKU-9001",
        "title": "Teclado Mecânico Sem Fio Switch Brown",
        "description": "Layout ABNT2, bateria recarregável USB-C, retroiluminação branca.",
        "price": 349.90
      },
      {
        "id": "SKU-9002",
        "title": "Mouse Ergonomico Vertical",
        "description": "Reduz tensão no pulso, 2400 DPI ajustável.",
        "price": 129.00
      }
    ]
  }'
```

* **Resposta (`202 Accepted`):**
```json
{
  "batch_id": "c7b8a910-4f12-4c8e-9b1a-7e3d2f1a8b90",
  "status": "PROCESSING",
  "total_items": 2,
  "created_at": "2026-11-19T19:30:00Z"
}
```

### 2. Consultar Status e Resultados do Lote
Retorna o progresso consolidado e os metadados enriquecidos pela IA.

* **URL:** `GET /v1/products/batch/{batch_id}`
* **Exemplo de Requisição:**
```bash
curl -X GET http://localhost:8080/v1/products/batch/c7b8a910-4f12-4c8e-9b1a-7e3d2f1a8b90
```

* **Resposta (`200 OK`):**
```json
{
  "batch_id": "c7b8a910-4f12-4c8e-9b1a-7e3d2f1a8b90",
  "status": "PARTIAL_FAILURE",
  "summary": {
    "total": 2,
    "processed": 2,
    "succeeded": 1,
    "failed": 1
  },
  "results": [
    {
      "product_id": "SKU-9001",
      "status": "SUCCESS",
      "enrichment": {
        "suggested_category": "Informática > Periféricos > Teclados",
        "tags": ["mecanico", "wireless", "usb-c", "abnt2"],
        "quality_score": 0.95
      },
      "attempts": 1
    },
    {
      "product_id": "SKU-9002",
      "status": "FAILED",
      "error": "ai service timeout after 3 retries",
      "attempts": 3
    }
  ]
}
```

---

## Observabilidade e Logs

A aplicação utiliza o pacote nativo `log/slog` configurado para saída em **JSON estruturado**, facilitando a ingestão por ferramentas como Google Cloud Logging, Datadog ou ELK Stack.

Exemplo de log emitido durante um retry:
```json
{
  "time": "2026-11-19T19:30:02.412Z",
  "level": "WARN",
  "msg": "ai enrichment attempt failed, scheduling retry",
  "batch_id": "c7b8a910-4f12-4c8e-9b1a-7e3d2f1a8b90",
  "product_id": "SKU-9002",
  "attempt": 1,
  "backoff_ms": 200,
  "error": "context deadline exceeded"
}
```

---

## Decisões Arquiteturais e Trade-offs

### 1. Bounded Worker Pool vs. Goroutines Ilimitadas
Disparar uma goroutine por produto recebido seria trivial, mas perigoso em produção: um pico de tráfego causaria esgotamento de conexões de rede e bloqueios por *Rate Limit (HTTP 429)* na API de IA. O uso de um *Worker Pool* fixo (5 workers) garante **backpressure** natural e previsibilidade de consumo de memória e CPU.

### 2. Resiliência com Context Timeout + Exponential Backoff
Cada chamada à interface `AIClient` recebe um `context.WithTimeout` derivado de 2 segundos. Falhas transitórias são absorvidas por até 3 tentativas com intervalos crescentes (`200ms -> 400ms -> 800ms`), evitando o efeito *thundering herd* sobre o serviço externo instável.

### 3. Evolução para Produção na Google Cloud Platform (GCP)
Nesta implementação, o enfileiramento ocorre via *Go Channels* e o estado reside em memória (`sync.RWMutex`). Em um cenário produtivo na **GCP**, onde containers no **Cloud Run** ou **GKE** podem escalar a zero ou reiniciar a qualquer momento sem perder mensagens em trânsito, a arquitetura evoluiria da seguinte forma:

* **Mensageria Durável (GCP Pub/Sub):** O endpoint `POST /v1/products/batch` publicaria cada item como uma mensagem em um tópico do **Pub/Sub** antes de retornar `202 Accepted`. Caso um worker falhe ou o pod reinicie, o *Ack Deadline* expira e o Pub/Sub reentrega a mensagem automaticamente, garantindo semântica *at-least-once*.
* **Dead Letter Queue (DLQ):** Itens que falharem após o limite de retries seriam encaminhados para um tópico de *Dead Letter* para reprocessamento posterior ou análise manual.
* **Persistência Distribuída (Cloud SQL / Firestore):** O estado dos lotes seria persistido em **PostgreSQL (Cloud SQL)** ou **Firestore**, permitindo que múltiplas réplicas stateless do serviço respondam às consultas `GET /v1/products/batch/{id}` com consistência.