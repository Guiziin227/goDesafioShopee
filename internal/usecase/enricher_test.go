package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guiziin227/enriquecedor-catalogo/internal/domain"
	"github.com/guiziin227/enriquecedor-catalogo/internal/infrastructure/repository"
)

// stubAIClient permite controlar falhas, latência e medir concorrência máxima nos testes
type stubAIClient struct {
	fn             func(ctx context.Context, p domain.Product) (*domain.EnrichmentData, error)
	activeCalls    atomic.Int32
	maxConcurrency atomic.Int32
}

func (s *stubAIClient) EnrichProduct(ctx context.Context, p domain.Product) (*domain.EnrichmentData, error) {
	current := s.activeCalls.Add(1)
	defer s.activeCalls.Add(-1)

	// Registra o pico máximo de goroutines executando simultaneamente
	for {
		max := s.maxConcurrency.Load()
		if current <= max || s.maxConcurrency.CompareAndSwap(max, current) {
			break
		}
	}

	return s.fn(ctx, p)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCatalogEnricher_ConcurrencyAndPoolLimit(t *testing.T) {
	repo := repository.NewInMemoryBatchRepository()
	stub := &stubAIClient{
		fn: func(ctx context.Context, p domain.Product) (*domain.EnrichmentData, error) {
			time.Sleep(40 * time.Millisecond)
			return &domain.EnrichmentData{
				SuggestedCategory: "Teste > Categoria",
				Tags:              []string{"tag1"},
				QualityScore:      0.9,
			}, nil
		},
	}

	const workerLimit = 5
	enricher := NewCatalogEnricher(repo, stub, discardLogger(), EnricherConfig{
		WorkerCount: workerLimit,
		AITimeout:   1 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	enricher.Start(ctx)
	defer enricher.Shutdown()

	// Cria um lote de 20 produtos (4x maior que o pool de 5 workers)
	products := make([]domain.Product, 20)
	for i := 0; i < 20; i++ {
		products[i] = domain.Product{
			ID:    string(rune('A' + i)),
			Title: "Produto Teste",
			Price: 50.0,
		}
	}

	batch, err := enricher.SubmitBatch(ctx, products)
	if err != nil {
		t.Fatalf("unexpected error submitting batch: %v", err)
	}

	// Polling até o lote finalizar
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := enricher.GetBatchStatus(ctx, batch.ID)
		if err != nil {
			t.Fatalf("unexpected error getting batch status: %v", err)
		}
		if status.Status == domain.BatchStatusCompleted {
			if status.Summary.Succeeded != 20 {
				t.Errorf("expected 20 succeeded, got %d", status.Summary.Succeeded)
			}
			if stub.maxConcurrency.Load() > workerLimit {
				t.Errorf("worker pool exceeded limit: max concurrency was %d, limit is %d", stub.maxConcurrency.Load(), workerLimit)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("timed out waiting for batch completion")
}

func TestCatalogEnricher_RetryAndRecovery(t *testing.T) {
	repo := repository.NewInMemoryBatchRepository()
	var attempts atomic.Int32

	stub := &stubAIClient{
		fn: func(ctx context.Context, p domain.Product) (*domain.EnrichmentData, error) {
			count := attempts.Add(1)
			if count < 3 {
				return nil, errors.New("temporary 429 rate limit")
			}
			return &domain.EnrichmentData{
				SuggestedCategory: "Recuperado",
				Tags:              []string{"ok"},
				QualityScore:      0.88,
			}, nil
		},
	}

	enricher := NewCatalogEnricher(repo, stub, discardLogger(), EnricherConfig{
		WorkerCount: 2,
		AITimeout:   500 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	enricher.Start(ctx)
	defer enricher.Shutdown()

	batch, err := enricher.SubmitBatch(ctx, []domain.Product{
		{ID: "RETRY-1", Title: "Item Instável", Price: 99.90},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, _ := enricher.GetBatchStatus(ctx, batch.ID)
		if status.Status == domain.BatchStatusCompleted {
			if status.Results[0].Attempts != 3 {
				t.Errorf("expected 3 attempts, got %d", status.Results[0].Attempts)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("timed out waiting for retry recovery")
}

func TestCatalogEnricher_TimeoutAndPartialFailure(t *testing.T) {
	repo := repository.NewInMemoryBatchRepository()

	stub := &stubAIClient{
		fn: func(ctx context.Context, p domain.Product) (*domain.EnrichmentData, error) {
			if p.ID == "SLOW-ITEM" {
				// Simula travamento maior que o AITimeout de 100ms
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &domain.EnrichmentData{SuggestedCategory: "Rápido", QualityScore: 0.9}, nil
		},
	}

	enricher := NewCatalogEnricher(repo, stub, discardLogger(), EnricherConfig{
		WorkerCount: 2,
		AITimeout:   100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	enricher.Start(ctx)
	defer enricher.Shutdown()

	batch, err := enricher.SubmitBatch(ctx, []domain.Product{
		{ID: "FAST-ITEM", Title: "Produto Rápido", Price: 10.0},
		{ID: "SLOW-ITEM", Title: "Produto Lento", Price: 20.0},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, _ := enricher.GetBatchStatus(ctx, batch.ID)
		if status.Status == domain.BatchStatusPartialFailure {
			if status.Summary.Succeeded != 1 || status.Summary.Failed != 1 {
				t.Errorf("expected 1 succeeded and 1 failed, got %+v", status.Summary)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("timed out waiting for PARTIAL_FAILURE status")
}
