package domain

import (
	"context"
	"time"
)

type BatchStatus string

const (
	BatchStatusPending        BatchStatus = "PENDING"
	BatchStatusPartialFailure BatchStatus = "PARTIAL_FAILURE"
	BatchStatusCompleted      BatchStatus = "COMPLETED"
)

type ItemStatus string

const (
	ItemStatusPending ItemStatus = "PENDING"
	ItemStatusSuccess ItemStatus = "SUCCESS"
	ItemStatusFailed  ItemStatus = "FAILED"
)

type Product struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
}

func (p *Product) Validate() error {
	if p.ID == "" || p.Title == "" || p.Price <= 0 {
		return ErrInvalidProduct
	}
	return nil
}

// EnrichmentData Representa o retorno gerado pela AI
type EnrichmentData struct {
	SuggestedCategory string   `json:"suggested_category"`
	Tags              []string `json:"tags"`
	QualityScore      float64  `json:"quality_score"`
}

// ProductResult guarda o estado e o resultado do processamento de um item
type ProductResult struct {
	ProductID  string          `json:"product_id"`
	Status     ItemStatus      `json:"status"`
	Enrichment *EnrichmentData `json:"enrichment,omitempty"`
	Error      string          `json:"error,omitempty"`
	Attempts   int             `json:"attempts"`
}

type BatchSummary struct {
	Total     int `json:"total"`
	Processed int `json:"processed"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

// Batch é a entidade raiz que agrupa a requisição e seus resultados
type Batch struct {
	ID        string          `json:"batch_id"`
	Status    BatchStatus     `json:"status"`
	Summary   BatchSummary    `json:"summary"`
	Results   []ProductResult `json:"results"`
	CreatedAt time.Time       `json:"created_at"`
}

// Definimos as interfaces aqui no domínio (Dependency Inversion Principle).
// As camadas externas (infrastructure) é que vão implementar esses contratos.

// BatchRepository define como os lotes são salvos e consultados
type BatchRepository interface {
	Save(ctx context.Context, batch *Batch) error
	GetByID(ctx context.Context, id string) (*Batch, error)
	UpdateItemResult(ctx context.Context, batchID string, result ProductResult) error
}

// AIClient define o contrato com qualquer provedor de IA (Mock, Gemini, OpenAI)
type AIClient interface {
	EnrichProduct(ctx context.Context, product Product) (*EnrichmentData, error)
}
