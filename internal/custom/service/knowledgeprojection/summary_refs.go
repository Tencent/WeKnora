package knowledgeprojection

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/service/summary"
)

// EvidenceReferenceProjection is the P4-02 evidence identity needed to audit
// summary evidence_refs without retaining transcript text.
type EvidenceReferenceProjection struct {
	EvidenceSentenceID   string   `json:"evidence_sentence_id"`
	ChunkRefs            []string `json:"chunk_refs"`
	StartMs              int      `json:"start_ms"`
	EndMs                int      `json:"end_ms"`
	SourceVideoID        string   `json:"source_video_id"`
	TranscriptGeneration string   `json:"transcript_generation"`
}

type SummaryReferenceScope struct {
	VideoID              string
	TranscriptGeneration string
	Pages                map[string]ObjectPageResult
	Evidence             map[string]EvidenceReferenceProjection
}

// SummaryKnowledgeBinding is the evidence-backed identity used when the
// knowledge layer enriches a summary block. A binding is valid only when the
// object page and evidence belong to the active video generation.
type SummaryKnowledgeBinding struct {
	WikiPageID           string
	SourceVideoID        string
	TranscriptGeneration string
	EvidenceIDs          []string
	ChunkRefs            []string
}

type SummaryKnowledgeCoverage struct {
	SectionID          string   `json:"section_id"`
	BlockID            string   `json:"block_id"`
	EvidenceRefCount   int      `json:"evidence_ref_count"`
	KnowledgePageIDs   []string `json:"knowledge_page_ids"`
	MissingEvidenceIDs []string `json:"missing_evidence_ids"`
	Complete           bool     `json:"complete"`
}

type SummaryReferenceAudit struct {
	SectionID     string   `json:"section_id"`
	BlockID       string   `json:"block_id"`
	KnowledgeRefs []string `json:"knowledge_refs"`
	EvidenceRefs  []string `json:"evidence_refs"`
	Status        string   `json:"status"`
	Reason        string   `json:"reason,omitempty"`
}

type SummaryDoubleReferenceResult struct {
	DraftAudits      []SummaryReferenceAudit `json:"draft_audits"`
	FinalAudits      []SummaryReferenceAudit `json:"final_audits"`
	DraftAvailable   bool                    `json:"draft_available"`
	FinalAvailable   bool                    `json:"final_available"`
	FinalPromotable  bool                    `json:"final_promotable"`
	FallbackRequired bool                    `json:"fallback_required"`
}

// AuditSummaryReferences checks that every reference in a summary resolves to
// a P4 page/evidence record. It does not inspect or persist summary content.
func AuditSummaryReferences(document summary.Document, scope SummaryReferenceScope) []SummaryReferenceAudit {
	return auditSummaryReferences(document, scope, true)
}

// AuditSummaryDraftReferences validates the evidence side of a draft while
// preserving the P1 contract that draft knowledge_refs may be empty.
func AuditSummaryDraftReferences(document summary.Document, scope SummaryReferenceScope) []SummaryReferenceAudit {
	return auditSummaryReferences(document, scope, false)
}

func auditSummaryReferences(document summary.Document, scope SummaryReferenceScope, requireKnowledgeRefs bool) []SummaryReferenceAudit {
	audits := make([]SummaryReferenceAudit, 0)
	for _, section := range document.Sections {
		for _, block := range section.Blocks {
			audit := SummaryReferenceAudit{SectionID: section.ID, BlockID: block.ID, KnowledgeRefs: append([]string(nil), block.KnowledgeRefs...)}
			for _, ref := range block.EvidenceRefs {
				audit.EvidenceRefs = append(audit.EvidenceRefs, ref.EvidenceSentenceID)
			}
			if err := validateSummaryBlockReferencesWithPolicy(block, scope, requireKnowledgeRefs); err != nil {
				audit.Status, audit.Reason = "rejected", err.Error()
			} else {
				audit.Status = "accepted"
			}
			audits = append(audits, audit)
		}
	}
	return audits
}

// ValidateSummaryDoubleReference validates draft/final independently, then
// applies the enhancement invariant. A failed knowledge layer can retain the
// draft, but it can never promote an invalid final or overwrite the draft.
func ValidateSummaryDoubleReference(draft, final *summary.Document, scope SummaryReferenceScope, videoType string) (SummaryDoubleReferenceResult, error) {
	result := SummaryDoubleReferenceResult{}
	if draft != nil {
		result.DraftAvailable = true
		if err := summary.ValidateStored(*draft, videoType); err != nil {
			result.FallbackRequired = true
			return result, fmt.Errorf("validate summary draft: %w", err)
		}
		result.DraftAudits = AuditSummaryDraftReferences(*draft, scope)
		if !allSummaryAuditsAccepted(result.DraftAudits) {
			result.FallbackRequired = true
		}
	}
	if final == nil {
		result.FinalPromotable = false
		return result, nil
	}
	result.FinalAvailable = true
	if err := summary.ValidateStored(*final, videoType); err != nil {
		result.FallbackRequired = true
		return result, fmt.Errorf("validate summary final: %w", err)
	}
	result.FinalAudits = AuditSummaryReferences(*final, scope)
	if !allSummaryAuditsAccepted(result.FinalAudits) {
		result.FallbackRequired = true
		return result, nil
	}
	if draft != nil {
		if err := summary.ValidateEnhancement(*draft, *final); err != nil {
			result.FallbackRequired = true
			return result, fmt.Errorf("validate summary enhancement: %w", err)
		}
	}
	result.FinalPromotable = true
	return result, nil
}

// BindSummaryKnowledgeReferences deterministically attaches real Wiki page
// IDs to summary blocks using their existing evidence refs. It never changes
// summary text or evidence anchors and fails closed when a block has no
// evidence-backed knowledge page.
func BindSummaryKnowledgeReferences(document *summary.Document, bindings []SummaryKnowledgeBinding, scope SummaryReferenceScope) error {
	if document == nil {
		return fmt.Errorf("summary document is nil")
	}
	byChunk, byEvidence, err := buildKnowledgeBindingIndexes(bindings, scope)
	if err != nil {
		return err
	}
	coverage := AnalyzeSummaryKnowledgeCoverage(*document, byChunk, byEvidence)
	for _, item := range coverage {
		if !item.Complete {
			return fmt.Errorf("block %s has no evidence-backed knowledge page; missing evidence IDs: %s", item.BlockID, strings.Join(item.MissingEvidenceIDs, ","))
		}
	}
	for sectionIndex := range document.Sections {
		for blockIndex := range document.Sections[sectionIndex].Blocks {
			block := &document.Sections[sectionIndex].Blocks[blockIndex]
			for _, item := range coverage {
				if item.SectionID == document.Sections[sectionIndex].ID && item.BlockID == block.ID {
					block.KnowledgeRefs = append([]string(nil), item.KnowledgePageIDs...)
				}
			}
		}
	}
	return nil
}

func buildKnowledgeBindingIndexes(bindings []SummaryKnowledgeBinding, scope SummaryReferenceScope) (map[string][]string, map[string][]string, error) {
	byChunk := make(map[string][]string)
	byEvidence := make(map[string][]string)
	for _, binding := range bindings {
		pageID := strings.TrimSpace(binding.WikiPageID)
		if pageID == "" {
			return nil, nil, fmt.Errorf("summary knowledge binding has empty Wiki page ID")
		}
		page, ok := scope.Pages[pageID]
		if !ok || page.WikiPageID != pageID || page.SourceVideoID != scope.VideoID || page.TranscriptGeneration != scope.TranscriptGeneration {
			return nil, nil, fmt.Errorf("summary knowledge binding references page outside active video generation")
		}
		for _, chunkID := range binding.ChunkRefs {
			byChunk[strings.TrimSpace(chunkID)] = appendUnique(byChunk[strings.TrimSpace(chunkID)], pageID)
		}
		for _, evidenceID := range binding.EvidenceIDs {
			byEvidence[strings.TrimSpace(evidenceID)] = appendUnique(byEvidence[strings.TrimSpace(evidenceID)], pageID)
		}
	}
	return byChunk, byEvidence, nil
}

func AnalyzeSummaryKnowledgeCoverage(document summary.Document, byChunk, byEvidence map[string][]string) []SummaryKnowledgeCoverage {
	coverage := make([]SummaryKnowledgeCoverage, 0)
	for _, section := range document.Sections {
		for _, block := range section.Blocks {
			item := SummaryKnowledgeCoverage{SectionID: section.ID, BlockID: block.ID, EvidenceRefCount: len(block.EvidenceRefs)}
			for _, evidence := range block.EvidenceRefs {
				matched := false
				for _, pageID := range byEvidence[strings.TrimSpace(evidence.EvidenceSentenceID)] {
					item.KnowledgePageIDs = appendUnique(item.KnowledgePageIDs, pageID)
					matched = true
				}
				for _, pageID := range byChunk[strings.TrimSpace(evidence.ChunkID)] {
					item.KnowledgePageIDs = appendUnique(item.KnowledgePageIDs, pageID)
					matched = true
				}
				if !matched {
					item.MissingEvidenceIDs = appendUnique(item.MissingEvidenceIDs, evidence.EvidenceSentenceID)
				}
			}
			sort.Strings(item.KnowledgePageIDs)
			item.Complete = item.EvidenceRefCount > 0 && len(item.MissingEvidenceIDs) == 0 && len(item.KnowledgePageIDs) > 0
			coverage = append(coverage, item)
		}
	}
	return coverage
}

// AuditSummaryKnowledgeCoverage reports exactly which evidence references can
// be backed by audited object pages, without mutating the summary document.
func AuditSummaryKnowledgeCoverage(document summary.Document, bindings []SummaryKnowledgeBinding, scope SummaryReferenceScope) ([]SummaryKnowledgeCoverage, error) {
	byChunk, byEvidence, err := buildKnowledgeBindingIndexes(bindings, scope)
	if err != nil {
		return nil, err
	}
	return AnalyzeSummaryKnowledgeCoverage(document, byChunk, byEvidence), nil
}

func validateSummaryBlockReferences(block summary.Block, scope SummaryReferenceScope) error {
	return validateSummaryBlockReferencesWithPolicy(block, scope, true)
}

func validateSummaryBlockReferencesWithPolicy(block summary.Block, scope SummaryReferenceScope, requireKnowledgeRefs bool) error {
	if requireKnowledgeRefs && len(block.KnowledgeRefs) == 0 {
		return fmt.Errorf("block %s has no knowledge page references", block.ID)
	}
	for _, pageID := range block.KnowledgeRefs {
		page, ok := scope.Pages[strings.TrimSpace(pageID)]
		if !ok || page.WikiPageID != strings.TrimSpace(pageID) {
			return fmt.Errorf("block %s references unknown Wiki page %q", block.ID, pageID)
		}
		if page.SourceVideoID != scope.VideoID || page.TranscriptGeneration != scope.TranscriptGeneration {
			return fmt.Errorf("block %s references a Wiki page outside the active video generation", block.ID)
		}
	}
	if len(block.EvidenceRefs) == 0 {
		return fmt.Errorf("block %s has no evidence references", block.ID)
	}
	for _, ref := range block.EvidenceRefs {
		projection, ok := scope.Evidence[ref.EvidenceSentenceID]
		if !ok {
			return fmt.Errorf("block %s references unknown evidence sentence %q", block.ID, ref.EvidenceSentenceID)
		}
		if projection.SourceVideoID != scope.VideoID || projection.TranscriptGeneration != scope.TranscriptGeneration ||
			ref.ChunkID == "" || !containsString(projection.ChunkRefs, ref.ChunkID) || ref.StartMs != projection.StartMs || ref.EndMs != projection.EndMs {
			return fmt.Errorf("block %s has an evidence reference that does not match P4-02", block.ID)
		}
	}
	return nil
}

func allSummaryAuditsAccepted(audits []SummaryReferenceAudit) bool {
	return len(audits) > 0 && func() bool {
		for _, audit := range audits {
			if audit.Status != "accepted" {
				return false
			}
		}
		return true
	}()
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(expected) {
			return true
		}
	}
	return false
}

func appendUnique(values []string, expected string) []string {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return values
	}
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return values
		}
	}
	return append(values, expected)
}

// StableSummaryAuditOrder keeps acceptance artifacts byte-stable when callers
// aggregate audits from multiple summary sections.
func StableSummaryAuditOrder(audits []SummaryReferenceAudit) {
	sort.SliceStable(audits, func(i, j int) bool {
		return audits[i].SectionID+"\x00"+audits[i].BlockID < audits[j].SectionID+"\x00"+audits[j].BlockID
	})
}
