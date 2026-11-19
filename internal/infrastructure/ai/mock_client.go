package ai

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/guiziin227/enriquecedor-catalogo/internal/domain"
)

type MockAIClient struct{}

func NewMockAIClient() *MockAIClient {
	return &MockAIClient{}
}

func (m *MockAIClient) EnrichProduct(ctx context.Context, product domain.Product) (*domain.EnrichmentData, error) {
	// Simula latência de rede entre 200ms e 600ms respeitando o cancelamento do context
	delay := time.Duration(200+rand.IntN(400)) * time.Millisecond

	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Simula 20% de taxa de erro intermitente para exercitar o Exponential Backoff
	if rand.Float64() < 0.20 {
		return nil, domain.ErrAIServiceFailure
	}

	return &domain.EnrichmentData{
		SuggestedCategory: "Geral > Catálogo > Produtos Diversos",
		Tags:              []string{"oferta", "catalogo", "novo"},
		QualityScore:      0.85,
	}, nil
}
