package repository

import (
	"context"
	"sync"

	"github.com/guiziin227/enriquecedor-catalogo/internal/domain"
)

type InMemoryBatchRepository struct {
	mu      sync.RWMutex
	batches map[string]*domain.Batch
}

func NewInMemoryBatchRepository() *InMemoryBatchRepository {
	return &InMemoryBatchRepository{
		batches: make(map[string]*domain.Batch),
	}
}

func (r *InMemoryBatchRepository) Save(ctx context.Context, batch *domain.Batch) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.batches[batch.ID] = batch
	return nil
}

func (r *InMemoryBatchRepository) GetByID(ctx context.Context, id string) (*domain.Batch, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	batch, exists := r.batches[id]
	if !exists {
		return nil, domain.ErrBatchNotFound
	}

	// Retornamos uma cópia rasa do struct e do slice para evitar race conditions
	// caso o serializador JSON leia o slice enquanto um worker escreve nele.
	batchCopy := *batch
	batchCopy.Results = make([]domain.ProductResult, len(batch.Results))
	copy(batchCopy.Results, batch.Results)

	return &batchCopy, nil
}

func (r *InMemoryBatchRepository) UpdateItemResult(ctx context.Context, batchID string, result domain.ProductResult) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	batch, exists := r.batches[batchID]
	if !exists {
		return domain.ErrBatchNotFound
	}

	// Atualiza o item correspondente pelo ProductID
	for i, item := range batch.Results {
		if item.ProductID == result.ProductID {
			batch.Results[i] = result
			break
		}
	}

	// Recalcula o sumário e verifica se o lote inteiro já terminou
	var processed, succeeded, failed int
	for _, item := range batch.Results {
		if item.Status == domain.ItemStatusSuccess {
			processed++
			succeeded++
		} else if item.Status == domain.ItemStatusFailed {
			processed++
			failed++
		}
	}

	batch.Summary.Processed = processed
	batch.Summary.Failed = failed
	batch.Summary.Succeeded = succeeded

	if processed == batch.Summary.Total {
		if failed > 0 {
			batch.Status = domain.BatchStatusPartialFailure
		} else {
			batch.Status = domain.BatchStatusCompleted
		}
	}

	return nil
}
