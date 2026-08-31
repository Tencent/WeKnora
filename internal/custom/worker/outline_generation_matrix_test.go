package worker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/service/outline"
)

func TestOutlineGenerationValidationMatrix(t *testing.T) {
	knownChunkIDs := map[string]struct{}{
		"chunk-1": {},
		"chunk-2": {},
		"chunk-3": {},
		"chunk-4": {},
	}
	scenarios := []struct {
		name          string
		response      func() string
		wantErrorText string
	}{
		{
			name: "valid output at duration boundary",
			response: func() string {
				return marshalOutlineForMatrix(t, validOutlineForMatrix())
			},
		},
		{
			name: "first chapter starts after zero",
			response: func() string {
				document := validOutlineForMatrix()
				document.Chapters[0].StartSeconds = 2
				return marshalOutlineForMatrix(t, document)
			},
			wantErrorText: "starts after video beginning",
		},
		{
			name: "chapters overlap",
			response: func() string {
				document := validOutlineForMatrix()
				document.Chapters[1].StartSeconds = 24
				return marshalOutlineForMatrix(t, document)
			},
			wantErrorText: "overlaps previous chapter",
		},
		{
			name: "outline misses transcript tail",
			response: func() string {
				document := validOutlineForMatrix()
				document.Chapters[3].EndSeconds = 98
				return marshalOutlineForMatrix(t, document)
			},
			wantErrorText: "does not cover effective transcript end",
		},
		{
			name: "unknown evidence chunk",
			response: func() string {
				document := validOutlineForMatrix()
				document.Chapters[0].EvidenceChunkIDs = []string{"unknown"}
				return marshalOutlineForMatrix(t, document)
			},
			wantErrorText: "references unknown chunk",
		},
		{
			name: "knowledge point outside chapter",
			response: func() string {
				document := validOutlineForMatrix()
				document.Chapters[0].KnowledgePoints[0].Seconds = 26
				return marshalOutlineForMatrix(t, document)
			},
			wantErrorText: "outside chapter time range",
		},
		{
			name: "unsupported schema version",
			response: func() string {
				document := validOutlineForMatrix()
				document.SchemaVersion = 0
				return marshalOutlineForMatrix(t, document)
			},
			wantErrorText: "schema_version must be 1",
		},
		{
			name: "too many chapters",
			response: func() string {
				document := validOutlineForMatrix()
				for index := 5; index <= 17; index++ {
					document.Chapters = append(document.Chapters, outline.Chapter{
						ChapterIndex:     index,
						ChapterTitle:     "章节",
						StartSeconds:     index * 5,
						EndSeconds:       index*5 + 1,
						ChapterSummary:   "补充内容",
						KnowledgePoints:  []outline.KnowledgePoint{{Title: "知识点", Seconds: index * 5, EvidenceChunkIDs: []string{"chunk-1"}}},
						EvidenceChunkIDs: []string{"chunk-1"},
					})
				}
				return marshalOutlineForMatrix(t, document)
			},
			wantErrorText: "too many chapters",
		},
		{
			name: "empty chapters",
			response: func() string {
				document := validOutlineForMatrix()
				document.Chapters = nil
				return marshalOutlineForMatrix(t, document)
			},
			wantErrorText: "at least one chapter",
		},
		{
			name: "malformed JSON",
			response: func() string {
				return `{"schema_version":1,"chapters":`
			},
			wantErrorText: "response does not contain a valid JSON object",
		},
	}

	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			var document outline.Document
			if err := parseLLMJSONResponse(scenario.response(), &document); err != nil {
				if scenario.wantErrorText == "response does not contain a valid JSON object" && strings.Contains(err.Error(), scenario.wantErrorText) {
					return
				}
				t.Fatalf("parse output: %v", err)
			}
			error := outline.ValidateWithTranscriptEnd(document, 100, 100, knownChunkIDs)
			if scenario.wantErrorText == "" {
				if error != nil {
					t.Fatalf("unexpected validation error: %v", error)
				}
				return
			}
			if error == nil || !strings.Contains(error.Error(), scenario.wantErrorText) {
				t.Fatalf("validation error = %v, want substring %q", error, scenario.wantErrorText)
			}
		})
	}
}

func validOutlineForMatrix() outline.Document {
	return outline.Document{
		SchemaVersion: 1,
		Chapters: []outline.Chapter{
			{ChapterIndex: 1, ChapterTitle: "开场定位", StartSeconds: 0, EndSeconds: 25, ChapterSummary: "说明学习主题和目标。", KnowledgePoints: []outline.KnowledgePoint{{Title: "明确学习目标", Seconds: 10, EvidenceChunkIDs: []string{"chunk-1"}}}, EvidenceChunkIDs: []string{"chunk-1"}},
			{ChapterIndex: 2, ChapterTitle: "学习方法", StartSeconds: 25, EndSeconds: 50, ChapterSummary: "介绍可执行的学习方法。", KnowledgePoints: []outline.KnowledgePoint{{Title: "拆解学习任务", Seconds: 30, EvidenceChunkIDs: []string{"chunk-2"}}}, EvidenceChunkIDs: []string{"chunk-2"}},
			{ChapterIndex: 3, ChapterTitle: "实践演示", StartSeconds: 50, EndSeconds: 75, ChapterSummary: "展示方法如何落地使用。", KnowledgePoints: []outline.KnowledgePoint{{Title: "直接动手实践", Seconds: 60, EvidenceChunkIDs: []string{"chunk-3"}}}, EvidenceChunkIDs: []string{"chunk-3"}},
			{ChapterIndex: 4, ChapterTitle: "总结行动", StartSeconds: 75, EndSeconds: 100, ChapterSummary: "总结重点并给出行动方向。", KnowledgePoints: []outline.KnowledgePoint{{Title: "持续追问复盘", Seconds: 90, EvidenceChunkIDs: []string{"chunk-4"}}}, EvidenceChunkIDs: []string{"chunk-4"}},
		},
	}
}

func marshalOutlineForMatrix(t *testing.T, document outline.Document) string {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal outline: %v", err)
	}
	return string(encoded)
}
