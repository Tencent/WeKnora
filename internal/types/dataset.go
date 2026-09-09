package types

// QAPair represents a complete QA example with question, related passages and answer
type QAPair struct {
	QID      int      // Question ID
	Question string   // Question text
	PIDs     []int    // Related passage IDs
	Passages []string // Passage texts
	AID      int      // Answer ID
	Answer   string   // Answer text
}

// EvaluationDataset describes one dataset discoverable by the evaluation UI.
// Full Parquet relationship validation still runs when an evaluation starts.
type EvaluationDataset struct {
	SchemaVersion      int      `json:"schema_version"`
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description,omitempty"`
	Language           string   `json:"language,omitempty"`
	Scenario           string   `json:"scenario,omitempty"`
	Source             string   `json:"source,omitempty"`
	License            string   `json:"license,omitempty"`
	CreatedAt          string   `json:"created_at,omitempty"`
	CoverageDimensions []string `json:"coverage_dimensions,omitempty"`
	Available          bool     `json:"available"`
	ValidationError    string   `json:"validation_error,omitempty"`
}
