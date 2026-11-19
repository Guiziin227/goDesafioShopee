package domain

import "errors"

var (
	ErrBatchNotFound    = errors.New("batch not found")
	ErrEmptyBatch       = errors.New("batch must contain at least one product")
	ErrBatchLimitExceed = errors.New("batch exceeds maximum limit of 50 products")
	ErrInvalidProduct   = errors.New("product must have valid id, title and price greater than zero")
	ErrAIServiceFailure = errors.New("ai service failed to enrich product")
)
