package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/guiziin227/enriquecedor-catalogo/internal/domain"
)

const (
	MaxBatchSize   = 50
	DefaultWorkers = 5
	MaxRetries     = 3
	BaseBackoff    = 200 * time.Millisecond
)

// enrichmentJob representa uma unidade de trabalho colocada na fila do Worker Pool
type enrichmentJob struct {
	BatchID string
	Product domain.Product
}

type EnricherConfig struct {
	WorkerCount int
	AITimeout   time.Duration
}

type CatalogEnricher struct {
	repo     domain.BatchRepository
	aiClient domain.AIClient
	logger   *slog.Logger
	config   EnricherConfig

	jobQueue chan enrichmentJob
	wg       sync.WaitGroup
}

func NewCatalogEnricher(
	repo domain.BatchRepository,
	aiClient domain.AIClient,
	logger *slog.Logger,
	cfg EnricherConfig,
) *CatalogEnricher {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = DefaultWorkers
	}
	if cfg.AITimeout <= 0 {
		cfg.AITimeout = 2 * time.Second
	}

	return &CatalogEnricher{
		repo:     repo,
		aiClient: aiClient,
		logger:   logger,
		config:   cfg,
		// Buffer de 500 posições para absorver picos sem bloquear o handler HTTP
		jobQueue: make(chan enrichmentJob, 500),
	}
}

// Start inicializa as N goroutines do Worker Pool em background
func (e *CatalogEnricher) Start(ctx context.Context) {
	e.logger.Info("starting worker pool", slog.Int("workers", e.config.WorkerCount))

	for i := 1; i <= e.config.WorkerCount; i++ {
		e.wg.Add(1)
		go e.worker(ctx, i)
	}
}

// Shutdown fecha a fila de entrada e aguarda todos os workers em execução terminarem (Graceful Shutdown)
func (e *CatalogEnricher) Shutdown() {
	e.logger.Info("shutting down worker pool, waiting for active jobs to finish...")
	close(e.jobQueue)
	e.wg.Wait()
	e.logger.Info("all workers stopped cleanly")
}

// SubmitBatch valida os produtos, persiste o lote inicial e enfileira os jobs sem bloquear
func (e *CatalogEnricher) SubmitBatch(ctx context.Context, products []domain.Product) (*domain.Batch, error) {
	if len(products) == 0 {
		return nil, domain.ErrEmptyBatch
	}
	if len(products) > MaxBatchSize {
		return nil, domain.ErrBatchLimitExceed
	}

	initialResults := make([]domain.ProductResult, len(products))
	for i, p := range products {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("product at index %d (%s): %w", i, p.ID, err)
		}
		initialResults[i] = domain.ProductResult{
			ProductID: p.ID,
			Status:    domain.ItemStatusPending,
		}
	}

	batch := &domain.Batch{
		ID:     uuid.NewString(),
		Status: domain.BatchStatusPending,
		Summary: domain.BatchSummary{
			Total: len(products),
		},
		Results:   initialResults,
		CreatedAt: time.Now().UTC(),
	}

	if err := e.repo.Save(ctx, batch); err != nil {
		return nil, fmt.Errorf("failed to persist initial batch: %w", err)
	}

	// Enfileira cada produto no channel para processamento assíncrono pelos workers
	for _, p := range products {
		e.jobQueue <- enrichmentJob{
			BatchID: batch.ID,
			Product: p,
		}
	}

	e.logger.Info("batch accepted and queued",
		slog.String("batch_id", batch.ID),
		slog.Int("total_items", len(products)),
	)

	return batch, nil
}

// GetBatchStatus consulta o progresso atual de um lote
func (e *CatalogEnricher) GetBatchStatus(ctx context.Context, batchID string) (*domain.Batch, error) {
	return e.repo.GetByID(ctx, batchID)
}

// worker é o loop executado por cada goroutine do pool
func (e *CatalogEnricher) worker(ctx context.Context, workerID int) {
	defer e.wg.Done()

	for job := range e.jobQueue {
		result := e.processWithRetry(ctx, workerID, job)

		// Usamos context.Background() no Save final para garantir que, mesmo durante
		// um shutdown, o resultado já processado pela IA não se perca ao atualizar o banco.
		if err := e.repo.UpdateItemResult(context.Background(), job.BatchID, result); err != nil {
			e.logger.Error("failed to update item result",
				slog.String("batch_id", job.BatchID),
				slog.String("product_id", job.Product.ID),
				slog.String("error", err.Error()),
			)
		}
	}
}

// processWithRetry executa a chamada à IA com timeout individual e Exponential Backoff
func (e *CatalogEnricher) processWithRetry(ctx context.Context, workerID int, job enrichmentJob) domain.ProductResult {
	var lastErr error

	for attempt := 1; attempt <= MaxRetries; attempt++ {
		start := time.Now()

		// Cria um contexto derivado com timeout estrito para ESTA tentativa
		attemptCtx, cancel := context.WithTimeout(ctx, e.config.AITimeout)
		enrichment, err := e.aiClient.EnrichProduct(attemptCtx, job.Product)
		cancel() // Sempre liberar recursos do timer imediatamente após o retorno

		durationMs := time.Since(start).Milliseconds()

		if err == nil {
			e.logger.Info("product enriched successfully",
				slog.Int("worker_id", workerID),
				slog.String("batch_id", job.BatchID),
				slog.String("product_id", job.Product.ID),
				slog.Int("attempt", attempt),
				slog.Int64("duration_ms", durationMs),
			)

			return domain.ProductResult{
				ProductID:  job.Product.ID,
				Status:     domain.ItemStatusSuccess,
				Enrichment: enrichment,
				Attempts:   attempt,
			}
		}

		lastErr = err
		e.logger.Warn("ai enrichment attempt failed",
			slog.Int("worker_id", workerID),
			slog.String("batch_id", job.BatchID),
			slog.String("product_id", job.Product.ID),
			slog.Int("attempt", attempt),
			slog.Int64("duration_ms", durationMs),
			slog.String("error", err.Error()),
		)

		// Se ainda houver tentativas restantes, aguarda o backoff exponencial (200ms, 400ms...)
		if attempt < MaxRetries {
			backoff := time.Duration(float64(BaseBackoff) * math.Pow(2, float64(attempt-1)))

			select {
			case <-time.After(backoff):
				// Continua para a próxima tentativa
			case <-ctx.Done():
				// A aplicação recebeu sinal de encerramento durante a espera
				return domain.ProductResult{
					ProductID: job.Product.ID,
					Status:    domain.ItemStatusFailed,
					Error:     "processing aborted due to service shutdown",
					Attempts:  attempt,
				}
			}
		}
	}

	return domain.ProductResult{
		ProductID: job.Product.ID,
		Status:    domain.ItemStatusFailed,
		Error:     fmt.Sprintf("failed after %d attempts: %v", MaxRetries, lastErr),
		Attempts:  MaxRetries,
	}
}
