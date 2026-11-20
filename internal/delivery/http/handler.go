package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/guiziin227/enriquecedor-catalogo/internal/domain"
	"github.com/guiziin227/enriquecedor-catalogo/internal/usecase"
)

type BatchHandler struct {
	enricher *usecase.CatalogEnricher
}

func NewBatchHandler(enricher *usecase.CatalogEnricher) *BatchHandler {
	return &BatchHandler{
		enricher: enricher,
	}
}

// RegisterRoutes registra os endpoints no ServeMux padrão do Go
func (h *BatchHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/products/batch", h.SubmitBatch)
	mux.HandleFunc("GET /v1/products/batch/{batch_id}", h.GetBatchStatus)
	mux.HandleFunc("GET /health", h.HealthCheck)
}

type submitBatchRequest struct {
	Products []domain.Product `json:"products"`
}

type submitBatchResponse struct {
	BatchID    string             `json:"batch_id"`
	Status     domain.BatchStatus `json:"status"`
	TotalItems int                `json:"total_items"`
	CreatedAt  string             `json:"created_at"`
}

func (h *BatchHandler) SubmitBatch(w http.ResponseWriter, r *http.Request) {
	var req submitBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}

	batch, err := h.enricher.SubmitBatch(r.Context(), req.Products)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrEmptyBatch),
			errors.Is(err, domain.ErrBatchLimitExceed),
			errors.Is(err, domain.ErrInvalidProduct):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		}
		return
	}

	resp := submitBatchResponse{
		BatchID:    batch.ID,
		Status:     batch.Status,
		TotalItems: batch.Summary.Total,
		CreatedAt:  batch.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	// Retorna 202 Accepted pois o processamento ocorre em background
	writeJSON(w, http.StatusAccepted, resp)
}

func (h *BatchHandler) GetBatchStatus(w http.ResponseWriter, r *http.Request) {
	batchID := r.PathValue("batch_id")
	if batchID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "batch_id is required"})
		return
	}

	batch, err := h.enricher.GetBatchStatus(r.Context(), batchID)
	if err != nil {
		if errors.Is(err, domain.ErrBatchNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, batch)
}

func (h *BatchHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
