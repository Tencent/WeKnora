// Package errors defines parse error code helpers.
package errors

const (
	// ErrCodeDocReaderTimeout — DocReader RPC exceeded
	// WEKNORA_DOCREADER_CALL_TIMEOUT (default 30m). Suggest splitting
	// large files or checking docreader load.
	ErrCodeDocReaderTimeout = "DOCREADER_TIMEOUT"

	// ErrCodeDocReaderUnavailable — no DocReader configured for the
	// requested file type / engine, or the service refused connection.
	ErrCodeDocReaderUnavailable = "DOCREADER_UNAVAILABLE"

	// ErrCodeDocReaderParseFailed — DocReader returned an explicit parse
	// error (encoding, corrupted file, OCR engine crash, ...).
	ErrCodeDocReaderParseFailed = "DOCREADER_PARSE_FAILED"

	// ErrCodeChunkingFailed — text chunking step itself failed (rare;
	// usually only on extreme-size inputs).
	ErrCodeChunkingFailed = "CHUNKING_FAILED"

	// ErrCodeEmbeddingRateLimit — embedding provider returned 429 or
	// equivalent. Suggest retrying later or scaling out.
	ErrCodeEmbeddingRateLimit = "EMBEDDING_RATE_LIMIT"

	// ErrCodeEmbeddingProviderFail — non-rate-limit embedding error
	// (auth, model not found, bad input). Usually permanent without
	// config changes.
	ErrCodeEmbeddingProviderFail = "EMBEDDING_PROVIDER_FAIL"

	// ErrCodeVectorStoreWriteFailed — chunks embedded fine but the
	// vector DB write failed (quota, connectivity, schema mismatch).
	ErrCodeVectorStoreWriteFailed = "VECTORSTORE_WRITE_FAILED"

	// ErrCodeMultimodalVLMFailed — a single image's OCR or caption call
	// failed. Note: per-image failures don't fail the whole parent;
	// see image_multimodal.go finalize-on-last-attempt.
	ErrCodeMultimodalVLMFailed = "MULTIMODAL_VLM_FAILED"

	// ErrCodeMultimodalAllFailed — every image task hit dead-letter.
	// The parent still completes (caption / OCR is optional content),
	// but stage status is marked failed so the UI can warn.
	ErrCodeMultimodalAllFailed = "MULTIMODAL_ALL_FAILED"

	// ErrCodeTaskTimeout — asynq retry budget exhausted. Used by the
	// dead-letter callback when promoting a task failure into a stage
	// failure. Distinct from DocReaderTimeout: this is the asynq-level
	// timeout (whole task), not the docreader-call-level timeout.
	ErrCodeTaskTimeout = "TASK_TIMEOUT"

	// ErrCodeUnknown — fallback when a wrapped error doesn't classify.
	// The full message is still recorded in error_detail so operators
	// can debug; the UI shows a generic "see admin" hint.
	ErrCodeUnknown = "UNKNOWN"
)
