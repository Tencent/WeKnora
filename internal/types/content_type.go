package types

import (
	"encoding/json"
	"fmt"
	"time"
)

type KnowledgeContentType string

const (
	KnowledgeContentTypeArticle      KnowledgeContentType = "article"
	KnowledgeContentTypeBook         KnowledgeContentType = "book"
	KnowledgeContentTypeWebpage      KnowledgeContentType = "webpage"
	KnowledgeContentTypeMeetingNotes KnowledgeContentType = "meeting_notes"
	KnowledgeContentTypeReport       KnowledgeContentType = "report"
	KnowledgeContentTypePresentation KnowledgeContentType = "presentation"
	KnowledgeContentTypeSpreadsheet  KnowledgeContentType = "spreadsheet"
	KnowledgeContentTypeManual       KnowledgeContentType = "manual"
	KnowledgeContentTypeOther        KnowledgeContentType = "other"
)

var knowledgeContentTypes = map[KnowledgeContentType]struct{}{
	KnowledgeContentTypeArticle:      {},
	KnowledgeContentTypeBook:         {},
	KnowledgeContentTypeWebpage:      {},
	KnowledgeContentTypeMeetingNotes: {},
	KnowledgeContentTypeReport:       {},
	KnowledgeContentTypePresentation: {},
	KnowledgeContentTypeSpreadsheet:  {},
	KnowledgeContentTypeManual:       {},
	KnowledgeContentTypeOther:        {},
}

func (t KnowledgeContentType) IsValid() bool {
	_, ok := knowledgeContentTypes[t]
	return ok
}

type ContentClassificationMetadata struct {
	SchemaVersion int                  `json:"schema_version"`
	Type          KnowledgeContentType `json:"type"`
	Source        string               `json:"source"`
	Confidence    float64              `json:"confidence,omitempty"`
	MatchedAt     time.Time            `json:"matched_at"`
}

func (k *Knowledge) SetContentClassification(classification ContentClassificationMetadata) error {
	if !classification.Type.IsValid() {
		return fmt.Errorf("unsupported content type %q", classification.Type)
	}
	raw, err := k.Metadata.Map()
	if err != nil || raw == nil {
		raw = map[string]interface{}{}
	}
	raw["content_classification"] = classification
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	k.Metadata = JSON(encoded)
	return nil
}

func (k *Knowledge) ContentClassification() (*ContentClassificationMetadata, error) {
	raw, err := k.Metadata.Map()
	if err != nil || raw == nil {
		return nil, err
	}
	value, ok := raw["content_classification"]
	if !ok {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var classification ContentClassificationMetadata
	if err := json.Unmarshal(encoded, &classification); err != nil {
		return nil, err
	}
	return &classification, nil
}
