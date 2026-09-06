package knowledge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateFirstStageWikiObjectPageRejectsInvalidTimeAndRelations(t *testing.T) {
	object := ClassifiedKnowledge{CandidateID: "object-1", SourceDocumentID: "doc-1", SourceVideoID: "video-1",
		TranscriptGeneration: "generation-1", PrimaryType: TypeConcept, Title: "网络效应", CoreContent: "定义",
		StructureFields: map[string]string{"DEFINITION": "定义", "mechanism": "机制"}, EvidenceIDs: []string{"ev-1"},
		ClassificationConfidence: 1, AuditStatus: "passed"}
	input := FirstStagePageInput{Object: object, TimeRange: "00:00:01.000-00:00:02.000", ChunkRefs: []string{"chunk-1"},
		FieldEvidence: map[string][]string{"DEFINITION": {"ev-1"}, "mechanism": {"ev-1"}}}
	render, err := RenderFirstStageObjectPage(input)
	require.NoError(t, err)
	expected := FirstStagePageExpectation{Object: object, TimeRange: input.TimeRange, VideoDurationMs: 3_000, ChunkRefs: input.ChunkRefs, SourceRefs: []string{"doc-1"}}
	require.NoError(t, ValidateFirstStageWikiObjectPage(render.Content, render.PageType, expected))

	invalidTime := expected
	invalidTime.TimeRange = "00:00:02.000-00:00:01.000"
	require.Error(t, ValidateFirstStageWikiObjectPage(render.Content, render.PageType, invalidTime))

	withRelation := replaceFirstStageValue(render.Content, "relations: []", "relations:\n  - relation_type: explains")
	require.ErrorContains(t, ValidateFirstStageWikiObjectPage(withRelation, render.PageType, expected), "relations must be empty")

	overDuration := expected
	overDuration.VideoDurationMs = 1_500
	require.ErrorContains(t, ValidateFirstStageWikiObjectPage(render.Content, render.PageType, overDuration), "time_range")
}

func TestRenderFirstStageObjectPageCoversFiveTypesAndEntitySubtypes(t *testing.T) {
	tests := []struct {
		primaryType   KnowledgeType
		entitySubType string
	}{
		{TypeEntity, "person"}, {TypeEntity, "organization"}, {TypeEntity, "product"},
		{TypeEntity, "technology"}, {TypeEntity, "industry"}, {TypeEntity, "place"},
		{TypeConcept, ""}, {TypeMethodology, ""}, {TypeCase, ""}, {TypeInsight, ""},
	}
	for _, test := range tests {
		name := string(test.primaryType)
		if test.entitySubType != "" {
			name += "/" + test.entitySubType
		}
		t.Run(name, func(t *testing.T) {
			framework, err := FrameworkFor(test.primaryType, test.entitySubType)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(framework.Fields), 2)
			structure := map[string]string{}
			fieldEvidence := map[string][]string{}
			for _, field := range framework.Fields {
				structure[strings.ToUpper(field.Key)] = field.Label + "值"
				fieldEvidence[field.Key] = []string{"ev-1"}
			}
			object := ClassifiedKnowledge{CandidateID: "object-" + strings.ReplaceAll(name, "/", "-"), SourceDocumentID: "doc-1",
				SourceVideoID: "video-1", TranscriptGeneration: "generation-1", PrimaryType: test.primaryType,
				EntitySubType: test.entitySubType, Title: "测试对象", CoreContent: "对象摘要", StructureFields: structure,
				EvidenceIDs: []string{"ev-1"}, ClassificationConfidence: 1, AuditStatus: "passed"}
			render, err := RenderFirstStageObjectPage(FirstStagePageInput{Object: object,
				TimeRange: "00:00:01.000-00:00:02.000", ChunkRefs: []string{"chunk-1"}, FieldEvidence: fieldEvidence})
			require.NoError(t, err)
			require.Equal(t, "index", render.PageType)
			require.NotContains(t, render.Content, "[[")
			require.NoError(t, ValidateFirstStageWikiObjectPage(render.Content, render.PageType, FirstStagePageExpectation{
				Object: object, TimeRange: "00:00:01.000-00:00:02.000", VideoDurationMs: 3_000,
				ChunkRefs: []string{"chunk-1"}, SourceRefs: []string{"doc-1"},
			}))
		})
	}
}

func replaceFirstStageValue(content, old, replacement string) string {
	for i := 0; i+len(old) <= len(content); i++ {
		if content[i:i+len(old)] == old {
			return content[:i] + replacement + content[i+len(old):]
		}
	}
	return content
}
