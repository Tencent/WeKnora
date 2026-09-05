package knowledge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

// Candidate is the metadata-only output of WeKnora candidate extraction.
// Source text is intentionally absent: classification receives citations and
// evidence IDs, while the full source document remains owned by WeKnora.
type Candidate struct {
	ID                   string              `json:"candidate_id"`
	SourceDocumentID     string              `json:"source_document_id"`
	SourceVideoID        string              `json:"source_video_id"`
	TranscriptGeneration string              `json:"transcript_generation"`
	Title                string              `json:"title"`
	CoreContent          string              `json:"core_content"`
	StructureFields      map[string]string   `json:"structure_fields,omitempty"`
	Citations            []CandidateCitation `json:"citations"`
	EvidenceIDs          []string            `json:"evidence_ids"`
}

type CandidateCitation struct {
	CitationID  string   `json:"citation_id"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type DocumentSectionIndex struct {
	SectionID   string   `json:"section_id"`
	Title       string   `json:"title"`
	StartMs     int      `json:"start_ms"`
	EndMs       int      `json:"end_ms"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// DocumentContext is the complete context allowed for classification. It does
// not carry subtitle blocks, transcript text, chunks, or page content.
type DocumentContext struct {
	SourceDocumentID     string                 `json:"source_document_id"`
	SourceVideoID        string                 `json:"source_video_id"`
	TranscriptGeneration string                 `json:"transcript_generation"`
	Summary              string                 `json:"summary"`
	Sections             []DocumentSectionIndex `json:"sections"`
	CandidateCitations   []CandidateCitation    `json:"candidate_citations"`
	EvidenceIDs          []string               `json:"evidence_ids"`
}

type ClassifiedKnowledge struct {
	CandidateID              string            `json:"candidate_id"`
	SourceDocumentID         string            `json:"source_document_id"`
	SourceVideoID            string            `json:"source_video_id"`
	TranscriptGeneration     string            `json:"transcript_generation"`
	PrimaryType              KnowledgeType     `json:"primary_type"`
	EntitySubType            string            `json:"entity_sub_type,omitempty"`
	Title                    string            `json:"title"`
	CoreContent              string            `json:"core_content"`
	StructureFields          map[string]string `json:"structure_fields"`
	EvidenceIDs              []string          `json:"evidence_ids"`
	ClassificationConfidence float64           `json:"classification_confidence"`
	AuditStatus              string            `json:"audit_status"`
}

// ValidateClassificationInput checks the relationship between the candidate
// and its document context before any model or deterministic classifier runs.
// This prevents a candidate from silently borrowing an evidence ID or source
// document from another video generation.
func ValidateClassificationInput(candidate Candidate, context DocumentContext) error {
	if err := candidate.Validate(); err != nil {
		return err
	}
	if err := context.Validate(); err != nil {
		return err
	}
	if candidate.SourceDocumentID != context.SourceDocumentID || candidate.SourceVideoID != context.SourceVideoID || candidate.TranscriptGeneration != context.TranscriptGeneration {
		return fmt.Errorf("candidate and document context identities do not match")
	}
	contextEvidence := make(map[string]struct{}, len(context.EvidenceIDs))
	for _, id := range context.EvidenceIDs {
		contextEvidence[id] = struct{}{}
	}
	for _, id := range candidate.EvidenceIDs {
		if _, ok := contextEvidence[id]; !ok {
			return fmt.Errorf("candidate evidence ID %q is not present in document context", id)
		}
	}
	for _, citation := range candidate.Citations {
		for _, id := range citation.EvidenceIDs {
			if _, ok := contextEvidence[id]; !ok {
				return fmt.Errorf("candidate citation evidence ID %q is not present in document context", id)
			}
		}
	}
	for _, section := range context.Sections {
		if err := ensureEvidenceSubset(section.EvidenceIDs, contextEvidence, "document context section"); err != nil {
			return err
		}
	}
	for _, citation := range context.CandidateCitations {
		if err := ensureEvidenceSubset(citation.EvidenceIDs, contextEvidence, "document context citation"); err != nil {
			return err
		}
	}
	return nil
}

// ValidateClassifiedKnowledge checks a classifier result against the exact
// candidate and context that produced it. It is the final pre-publish input
// gate; type-specific minimum fields are applied by later P3 steps.
func ValidateClassifiedKnowledge(candidate Candidate, context DocumentContext, classified ClassifiedKnowledge) error {
	if err := ValidateClassificationInput(candidate, context); err != nil {
		return err
	}
	if err := classified.Validate(); err != nil {
		return err
	}
	if classified.CandidateID != candidate.ID {
		return fmt.Errorf("classified knowledge candidate_id does not match candidate")
	}
	if classified.SourceDocumentID != context.SourceDocumentID || classified.SourceVideoID != context.SourceVideoID || classified.TranscriptGeneration != context.TranscriptGeneration {
		return fmt.Errorf("classified knowledge identities do not match document context")
	}
	contextEvidence := make(map[string]struct{}, len(context.EvidenceIDs))
	for _, id := range context.EvidenceIDs {
		contextEvidence[id] = struct{}{}
	}
	if err := ensureEvidenceSubset(classified.EvidenceIDs, contextEvidence, "classified knowledge"); err != nil {
		return err
	}
	candidateEvidence := make(map[string]struct{}, len(candidate.EvidenceIDs))
	for _, id := range candidate.EvidenceIDs {
		candidateEvidence[id] = struct{}{}
	}
	return ensureEvidenceSubset(classified.EvidenceIDs, candidateEvidence, "classified knowledge")
}

func DecodeCandidate(data []byte) (Candidate, error) {
	var value Candidate
	if err := decodeStrict(data, &value); err != nil {
		return Candidate{}, fmt.Errorf("decode candidate: %w", err)
	}
	if err := value.Validate(); err != nil {
		return Candidate{}, err
	}
	return value, nil
}

func DecodeDocumentContext(data []byte) (DocumentContext, error) {
	var value DocumentContext
	if err := decodeStrict(data, &value); err != nil {
		return DocumentContext{}, fmt.Errorf("decode document context: %w", err)
	}
	if err := value.Validate(); err != nil {
		return DocumentContext{}, err
	}
	return value, nil
}

func DecodeClassifiedKnowledge(data []byte) (ClassifiedKnowledge, error) {
	var value ClassifiedKnowledge
	if err := decodeStrict(data, &value); err != nil {
		return ClassifiedKnowledge{}, fmt.Errorf("decode classified knowledge: %w", err)
	}
	if err := value.Validate(); err != nil {
		return ClassifiedKnowledge{}, err
	}
	return value, nil
}

func (c Candidate) Validate() error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.SourceDocumentID) == "" || strings.TrimSpace(c.SourceVideoID) == "" || strings.TrimSpace(c.TranscriptGeneration) == "" || strings.TrimSpace(c.Title) == "" {
		return fmt.Errorf("candidate identity is incomplete")
	}
	if len(c.EvidenceIDs) == 0 {
		return fmt.Errorf("candidate evidence_ids must not be empty")
	}
	if err := validateEvidenceIDs(c.EvidenceIDs); err != nil {
		return fmt.Errorf("candidate: %w", err)
	}
	candidateEvidence := make(map[string]struct{}, len(c.EvidenceIDs))
	for _, id := range c.EvidenceIDs {
		candidateEvidence[id] = struct{}{}
	}
	citationIDs := make(map[string]struct{}, len(c.Citations))
	for _, citation := range c.Citations {
		if strings.TrimSpace(citation.CitationID) == "" || len(citation.EvidenceIDs) == 0 {
			return fmt.Errorf("candidate citation identity or evidence is incomplete")
		}
		if _, exists := citationIDs[citation.CitationID]; exists {
			return fmt.Errorf("duplicate candidate citation ID %q", citation.CitationID)
		}
		citationIDs[citation.CitationID] = struct{}{}
		if err := validateEvidenceIDs(citation.EvidenceIDs); err != nil {
			return fmt.Errorf("candidate citation: %w", err)
		}
		for _, id := range citation.EvidenceIDs {
			if _, ok := candidateEvidence[id]; !ok {
				return fmt.Errorf("candidate citation evidence ID %q is not present in candidate evidence_ids", id)
			}
		}
	}
	if err := rejectReservedStructureKeys(c.StructureFields); err != nil {
		return err
	}
	return nil
}

func (c DocumentContext) Validate() error {
	if strings.TrimSpace(c.SourceDocumentID) == "" || strings.TrimSpace(c.SourceVideoID) == "" || strings.TrimSpace(c.TranscriptGeneration) == "" {
		return fmt.Errorf("document context identity is incomplete")
	}
	if strings.TrimSpace(c.Summary) == "" {
		return fmt.Errorf("document context summary is required")
	}
	if len(c.EvidenceIDs) == 0 {
		return fmt.Errorf("document context evidence_ids must not be empty")
	}
	if err := validateEvidenceIDs(c.EvidenceIDs); err != nil {
		return fmt.Errorf("document context: %w", err)
	}
	contextEvidence := make(map[string]struct{}, len(c.EvidenceIDs))
	for _, id := range c.EvidenceIDs {
		contextEvidence[id] = struct{}{}
	}
	for _, section := range c.Sections {
		if strings.TrimSpace(section.SectionID) == "" || strings.TrimSpace(section.Title) == "" || section.StartMs < 0 || section.EndMs <= section.StartMs || len(section.EvidenceIDs) == 0 {
			return fmt.Errorf("document context section is incomplete")
		}
		if err := validateEvidenceIDs(section.EvidenceIDs); err != nil {
			return fmt.Errorf("document context section: %w", err)
		}
		if err := ensureEvidenceSubset(section.EvidenceIDs, contextEvidence, "document context section"); err != nil {
			return err
		}
	}
	citationIDs := make(map[string]struct{}, len(c.CandidateCitations))
	for _, citation := range c.CandidateCitations {
		if strings.TrimSpace(citation.CitationID) == "" || len(citation.EvidenceIDs) == 0 {
			return fmt.Errorf("document context citation is incomplete")
		}
		if _, exists := citationIDs[citation.CitationID]; exists {
			return fmt.Errorf("duplicate document context citation ID %q", citation.CitationID)
		}
		citationIDs[citation.CitationID] = struct{}{}
		if err := validateEvidenceIDs(citation.EvidenceIDs); err != nil {
			return fmt.Errorf("document context citation: %w", err)
		}
		if err := ensureEvidenceSubset(citation.EvidenceIDs, contextEvidence, "document context citation"); err != nil {
			return err
		}
	}
	return nil
}

func (k ClassifiedKnowledge) Validate() error {
	if strings.TrimSpace(k.CandidateID) == "" || strings.TrimSpace(k.SourceDocumentID) == "" || strings.TrimSpace(k.SourceVideoID) == "" || strings.TrimSpace(k.TranscriptGeneration) == "" || strings.TrimSpace(k.Title) == "" || strings.TrimSpace(k.CoreContent) == "" {
		return fmt.Errorf("classified knowledge identity or content is incomplete")
	}
	if !IsKnowledgeType(k.PrimaryType) {
		return fmt.Errorf("unsupported primary_type: %q", k.PrimaryType)
	}
	if k.PrimaryType == TypeEntity && !IsEntitySubType(strings.TrimSpace(k.EntitySubType)) {
		return fmt.Errorf("entity_sub_type is required for entity")
	}
	if k.PrimaryType != TypeEntity && strings.TrimSpace(k.EntitySubType) != "" {
		return fmt.Errorf("entity_sub_type is only valid for entity")
	}
	if len(k.EvidenceIDs) == 0 {
		return fmt.Errorf("classified knowledge evidence_ids must not be empty")
	}
	if err := validateEvidenceIDs(k.EvidenceIDs); err != nil {
		return err
	}
	if math.IsNaN(k.ClassificationConfidence) || math.IsInf(k.ClassificationConfidence, 0) || k.ClassificationConfidence <= 0 || k.ClassificationConfidence > 1 {
		return fmt.Errorf("classification_confidence must be in (0,1]")
	}
	if _, ok := map[string]struct{}{"pending": {}, "passed": {}, "rejected": {}}[strings.ToLower(strings.TrimSpace(k.AuditStatus))]; !ok {
		return fmt.Errorf("audit_status must be pending, passed, or rejected")
	}
	if err := rejectReservedStructureKeys(k.StructureFields); err != nil {
		return err
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateEvidenceIDs(ids []string) error {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("evidence ID must not be empty")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate evidence ID %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func rejectReservedStructureKeys(fields map[string]string) error {
	for key := range fields {
		normalized := strings.ToLower(strings.TrimSpace(key))
		for _, reserved := range []string{
			"subtitle", "transcript", "chunk", "evidence_text", "raw_text", "block",
			"wiki_page_id", "target_wiki_page_id", "graph_id", "neo4j", "page_type",
			"write_status", "wiki_status", "relation_id",
		} {
			if strings.Contains(normalized, reserved) {
				return fmt.Errorf("structure_fields.%s is not allowed in classification context", key)
			}
		}
	}
	return nil
}

func ensureEvidenceSubset(ids []string, allowed map[string]struct{}, owner string) error {
	for _, id := range ids {
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("%s evidence ID %q is not present in document context", owner, id)
		}
	}
	return nil
}
