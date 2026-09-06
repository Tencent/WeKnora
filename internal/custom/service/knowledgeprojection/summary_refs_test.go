package knowledgeprojection

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/service/summary"
	"github.com/stretchr/testify/require"
)

func summaryScope() SummaryReferenceScope {
	return SummaryReferenceScope{
		VideoID: "video-1", TranscriptGeneration: "gen-1",
		Pages:    map[string]ObjectPageResult{"page-1": {WikiPageID: "page-1", SourceVideoID: "video-1", TranscriptGeneration: "gen-1"}},
		Evidence: map[string]EvidenceReferenceProjection{"ev-1": {EvidenceSentenceID: "ev-1", ChunkRefs: []string{"chunk-1"}, StartMs: 1000, EndMs: 2000, SourceVideoID: "video-1", TranscriptGeneration: "gen-1"}},
	}
}

func validSummary() summary.Document {
	framework, _ := summary.Framework("general")
	document := summary.Document{SchemaVersion: summary.SchemaVersion, VideoType: "general"}
	for _, section := range framework {
		document.Sections = append(document.Sections, summary.Section{
			ID: section.ID, Title: section.Title,
			Blocks: []summary.Block{{
				ID: section.ID + "-block", Kind: summary.BlockKindParagraph, Text: "内容",
				EvidenceChunkIDs: []string{"chunk-1"}, KnowledgeRefs: []string{"page-1"},
				EvidenceRefs: []summary.EvidenceRef{{ChunkID: "chunk-1", EvidenceSentenceID: "ev-1", StartMs: 1000, EndMs: 2000}},
				Evidence:     []summary.Evidence{{ChunkID: "chunk-1", EvidenceSentenceID: "ev-1", StartSeconds: 1, EndSeconds: 2, Timestamp: "00:01-00:02", TranscriptSnippet: "原文"}},
			}},
		})
	}
	return document
}

func TestAuditSummaryReferencesRejectsUnknownPageAndEvidence(t *testing.T) {
	document := validSummary()
	document.Sections[0].Blocks[0].KnowledgeRefs = []string{"missing-page"}
	audits := AuditSummaryReferences(document, summaryScope())
	require.Equal(t, "rejected", audits[0].Status)
	require.Contains(t, audits[0].Reason, "unknown Wiki page")
	document = validSummary()
	document.Sections[0].Blocks[0].EvidenceRefs[0].EvidenceSentenceID = "missing-evidence"
	audits = AuditSummaryReferences(document, summaryScope())
	require.Equal(t, "rejected", audits[0].Status)
	require.Contains(t, audits[0].Reason, "unknown evidence")
}

func TestValidateSummaryDoubleReferencePromotesOnlyValidFinal(t *testing.T) {
	draft, final := validSummary(), validSummary()
	result, err := ValidateSummaryDoubleReference(&draft, &final, summaryScope(), "general")
	require.NoError(t, err)
	require.True(t, result.DraftAvailable)
	require.True(t, result.FinalPromotable)

	final.Sections[0].Blocks[0].KnowledgeRefs = []string{"missing-page"}
	result, err = ValidateSummaryDoubleReference(&draft, &final, summaryScope(), "general")
	require.NoError(t, err)
	require.False(t, result.FinalPromotable)
	require.True(t, result.FallbackRequired)
}

func TestValidateSummaryDoubleReferenceAllowsDraftWithoutKnowledgeRefs(t *testing.T) {
	draft, final := validSummary(), validSummary()
	draft.Sections[0].Blocks[0].KnowledgeRefs = nil
	result, err := ValidateSummaryDoubleReference(&draft, &final, summaryScope(), "general")
	require.NoError(t, err)
	require.True(t, result.DraftAvailable)
	require.True(t, result.FinalPromotable)
	require.Equal(t, "accepted", result.DraftAudits[0].Status)
}

func TestBindSummaryKnowledgeReferencesUsesEvidenceBackedPageIDs(t *testing.T) {
	document := validSummary()
	scope := summaryScope()
	scope.Pages["page-2"] = ObjectPageResult{WikiPageID: "page-2", SourceVideoID: "video-1", TranscriptGeneration: "gen-1"}
	err := BindSummaryKnowledgeReferences(&document, []SummaryKnowledgeBinding{{
		WikiPageID: "page-2", EvidenceIDs: []string{"ev-1"}, ChunkRefs: []string{"chunk-1"},
	}}, scope)
	require.NoError(t, err)
	require.Equal(t, []string{"page-2"}, document.Sections[0].Blocks[0].KnowledgeRefs)
}

func TestBindSummaryKnowledgeReferencesRejectsUnmappedBlock(t *testing.T) {
	document := validSummary()
	scope := summaryScope()
	err := BindSummaryKnowledgeReferences(&document, nil, scope)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no evidence-backed knowledge page")
}

func TestBindSummaryKnowledgeReferencesRejectsPartialEvidenceCoverage(t *testing.T) {
	document := validSummary()
	block := &document.Sections[0].Blocks[0]
	block.EvidenceRefs = append(block.EvidenceRefs, summary.EvidenceRef{
		ChunkID: "chunk-2", EvidenceSentenceID: "ev-2", StartMs: 3000, EndMs: 4000,
	})
	scope := summaryScope()
	scope.Evidence["ev-2"] = EvidenceReferenceProjection{
		EvidenceSentenceID: "ev-2", ChunkRefs: []string{"chunk-2"}, StartMs: 3000, EndMs: 4000,
		SourceVideoID: "video-1", TranscriptGeneration: "gen-1",
	}
	err := BindSummaryKnowledgeReferences(&document, []SummaryKnowledgeBinding{{
		WikiPageID: "page-1", EvidenceIDs: []string{"ev-1"}, ChunkRefs: []string{"chunk-1"},
	}}, scope)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ev-2")
	require.Equal(t, []string{"page-1"}, block.KnowledgeRefs)
}

func TestValidateSummaryDoubleReferenceRejectsChangedEvidenceAnchors(t *testing.T) {
	draft, final := validSummary(), validSummary()
	final.Sections[0].Blocks[0].EvidenceChunkIDs = []string{"chunk-other"}
	_, err := ValidateSummaryDoubleReference(&draft, &final, summaryScope(), "general")
	require.Error(t, err)
	require.Contains(t, err.Error(), "summary final")
}
