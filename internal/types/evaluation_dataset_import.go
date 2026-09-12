package types

// EvaluationDatasetImportInput creates a tenant dataset and its first version.
// RequestID identifies retries within one tenant.
type EvaluationDatasetImportInput struct {
	RequestID   string                         `json:"request_id"`
	Name        string                         `json:"name"`
	Description string                         `json:"description"`
	Content     *EvaluationDatasetVersionInput `json:"content"`
}

// EvaluationDatasetImportResult includes the immutable initial version on retries.
type EvaluationDatasetImportResult struct {
	Dataset  *EvaluationDataset        `json:"dataset"`
	Version  *EvaluationDatasetVersion `json:"version"`
	Replayed bool                      `json:"replayed"`
}
