package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// EvaluationExperimentSchemaVersion is the experiment snapshot schema version.
// The snapshot is frozen before a task enters Pending and never changes
// afterwards; lifecycle compare-and-swap updates must not modify it.
const EvaluationExperimentSchemaVersion = 1

// EvaluationOptions is the M3 creation option set: the four legacy fields
// plus an optional dataset version binding, configuration overrides, and a
// seed pointer distinguishing "not provided" from an explicit seed=0.
type EvaluationOptions struct {
	DatasetID        string
	KnowledgeBaseID  string
	ChatModelID      string
	RerankModelID    string
	DatasetVersionID string
	Seed             *int
	Configuration    *EvaluationConfigurationOverrides
}

// EvaluationGenerationOverrides carries optional generation parameter
// overrides from the creation request. Nil fields keep the resolved default.
type EvaluationGenerationOverrides struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
}

// EvaluationRetrievalOverrides carries optional retrieval request fields.
// Omitted fields preserve resolved defaults; explicit zero values are applied.
type EvaluationRetrievalOverrides struct {
	VectorThreshold  *float64 `json:"vector_threshold,omitempty"`
	KeywordThreshold *float64 `json:"keyword_threshold,omitempty"`
	EmbeddingTopK    *int     `json:"embedding_top_k,omitempty"`
}

// EvaluationRerankOverrides carries optional rerank request fields.
// Omitted fields preserve resolved defaults; explicit zero values are applied.
type EvaluationRerankOverrides struct {
	RerankTopK      *int     `json:"rerank_top_k,omitempty"`
	RerankThreshold *float64 `json:"rerank_threshold,omitempty"`
}

// EvaluationConfigurationOverrides carries optional request parameters.
// The resolved values enter the experiment snapshot and its hash.
type EvaluationConfigurationOverrides struct {
	Retrieval  *EvaluationRetrievalOverrides  `json:"retrieval,omitempty"`
	Rerank     *EvaluationRerankOverrides     `json:"rerank,omitempty"`
	Generation *EvaluationGenerationOverrides `json:"generation,omitempty"`
}

// EvaluationDatasetSnapshot pins the dataset identity of one experiment.
type EvaluationDatasetSnapshot struct {
	DatasetID        string `json:"dataset_id"`
	DatasetVersionID string `json:"dataset_version_id"`
	VersionNumber    int    `json:"version_number"`
	ArtifactSHA256   string `json:"artifact_sha256"`
	ContentSHA256    string `json:"content_sha256"`
}

// EvaluationModelSnapshot pins one model with its sanitized behavior
// configuration fingerprint. Config SHA-256 never covers API keys, app
// secrets, custom authorization headers, or credentials embedded in URLs.
type EvaluationModelSnapshot struct {
	ConfigVersion int       `json:"config_version,omitempty"`
	ID            string    `json:"id"`
	UpstreamName  string    `json:"upstream_name"`
	Type          string    `json:"type"`
	Source        string    `json:"source"`
	Provider      string    `json:"provider"`
	InterfaceType string    `json:"interface_type"`
	ConfigSHA256  string    `json:"config_sha256"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// EvaluationModelSetSnapshot pins the four model roles of one experiment.
// Rerank is optional; an absent model stays null instead of a zero object.
type EvaluationModelSetSnapshot struct {
	Embedding *EvaluationModelSnapshot `json:"embedding"`
	Chat      *EvaluationModelSnapshot `json:"chat"`
	Rerank    *EvaluationModelSnapshot `json:"rerank"`
	Summary   *EvaluationModelSnapshot `json:"summary"`
}

// Seed support states recorded in the resolved generation configuration.
const (
	EvaluationSeedSupportApplied      = "applied"
	EvaluationSeedSupportUnsupported  = "unsupported"
	EvaluationSeedSupportNotRequested = "not_requested"
	EvaluationSeedSupportUnavailable  = "unavailable"
)

// EvaluationGenerationSnapshot is the resolved generation configuration.
type EvaluationGenerationSnapshot struct {
	Seed         *int   `json:"seed"`
	SeedProvided bool   `json:"seed_provided"`
	SeedSupport  string `json:"seed_support"`

	MaxTokens           int     `json:"max_tokens"`
	MaxCompletionTokens int     `json:"max_completion_tokens"`
	Temperature         float64 `json:"temperature"`
	TopP                float64 `json:"top_p"`
	TopK                int     `json:"top_k"`
	RepeatPenalty       float64 `json:"repeat_penalty"`
	FrequencyPenalty    float64 `json:"frequency_penalty"`
	PresencePenalty     float64 `json:"presence_penalty"`
}

// EvaluationRetrievalSnapshot is the resolved retrieval configuration.
type EvaluationRetrievalSnapshot struct {
	VectorThreshold  float64 `json:"vector_threshold"`
	KeywordThreshold float64 `json:"keyword_threshold"`
	EmbeddingTopK    int     `json:"embedding_top_k"`
}

// EvaluationRerankSnapshot is the resolved rerank configuration.
type EvaluationRerankSnapshot struct {
	RerankTopK      int     `json:"rerank_top_k"`
	RerankThreshold float64 `json:"rerank_threshold"`
}

// EvaluationConfigurationSnapshot carries every resolved parameter group.
type EvaluationConfigurationSnapshot struct {
	Retrieval  EvaluationRetrievalSnapshot  `json:"retrieval"`
	Rerank     EvaluationRerankSnapshot     `json:"rerank"`
	Generation EvaluationGenerationSnapshot `json:"generation"`
	Chunking   json.RawMessage              `json:"chunking"`
	Indexing   json.RawMessage              `json:"indexing"`
}

// EvaluationCodeSnapshot pins the code version that resolved the experiment.
type EvaluationCodeSnapshot struct {
	Version   string `json:"version"`
	CommitID  string `json:"commit_id"`
	Dirty     *bool  `json:"dirty"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// EvaluationEnvironmentSnapshot is the sanitized environment whitelist:
// exactly these fields, never the full environment variable set.
type EvaluationEnvironmentSnapshot struct {
	Edition  string `json:"edition"`
	GOOS     string `json:"goos"`
	GOARCH   string `json:"goarch"`
	DBDriver string `json:"db_driver"`
}

// Reproducibility levels reported by the snapshot.
const (
	EvaluationReproducibilityAuditable = "auditable"
	EvaluationReproducibilityPartial   = "partial"
)

// EvaluationReproducibilitySnapshot classifies how faithfully a run can be
// replayed and lists human-readable warnings.
type EvaluationReproducibilitySnapshot struct {
	Level    string   `json:"level"`
	Warnings []string `json:"warnings"`
}

// EvaluationExperimentSnapshot is the schema-version-1 immutable experiment
// manifest stored on evaluation_tasks.experiment_snapshot.
type EvaluationExperimentSnapshot struct {
	SchemaVersion int `json:"schema_version"`

	Dataset EvaluationDatasetSnapshot `json:"dataset"`
	// SourceKnowledgeBaseID is the original knowledge base ID from the
	// creation request (null when absent). It participates in the experiment
	// hash and is the only authorization input for API-key KB allow-lists;
	// it never carries the temporary evaluation KB ID.
	SourceKnowledgeBaseID *string `json:"source_knowledge_base_id"`

	Models          EvaluationModelSetSnapshot        `json:"models"`
	Configuration   EvaluationConfigurationSnapshot   `json:"configuration"`
	MetricPlan      *EvaluationMetricPlanSnapshot     `json:"metric_plan"`
	Code            EvaluationCodeSnapshot            `json:"code"`
	Environment     EvaluationEnvironmentSnapshot     `json:"environment"`
	Reproducibility EvaluationReproducibilitySnapshot `json:"reproducibility"`
}

// EvaluationSourceKnowledgeBaseID reads the immutable authorization source
// from a persisted experiment snapshot.
func EvaluationSourceKnowledgeBaseID(snapshot JSON) (string, bool) {
	if len(bytes.TrimSpace(snapshot)) == 0 {
		return "", false
	}
	var experiment EvaluationExperimentSnapshot
	if err := json.Unmarshal(snapshot, &experiment); err != nil || experiment.SourceKnowledgeBaseID == nil {
		return "", false
	}
	source := strings.TrimSpace(*experiment.SourceKnowledgeBaseID)
	return source, source != ""
}

// CanonicalJSON serializes the snapshot with deterministic bytes: fixed field
// order and no HTML escaping. Concurrent completion order must never change
// these bytes.
func (s *EvaluationExperimentSnapshot) CanonicalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// SHA256 computes the canonical experiment hash persisted as
// evaluation_tasks.experiment_sha256.
func (s *EvaluationExperimentSnapshot) SHA256() (string, error) {
	canonical, err := s.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// Clone deep-copies the snapshot including nested plans and raw sections.
func (s *EvaluationExperimentSnapshot) Clone() *EvaluationExperimentSnapshot {
	if s == nil {
		return nil
	}
	encoded, err := s.CanonicalJSON()
	if err != nil {
		return nil
	}
	var clone EvaluationExperimentSnapshot
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil
	}
	return &clone
}
