package types

import "testing"

func TestHasKnowledgeRetrievalScope(t *testing.T) {
	tests := []struct {
		name             string
		searchTargets    SearchTargets
		knowledgeBaseIDs []string
		knowledgeIDs     []string
		want             bool
	}{
		{name: "empty", want: false},
		{name: "knowledge base IDs", knowledgeBaseIDs: []string{"kb-1"}, want: true},
		{name: "knowledge IDs", knowledgeIDs: []string{"doc-1"}, want: true},
		{
			name: "tag-only search target",
			searchTargets: SearchTargets{
				{
					Type:            SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID: "kb-1",
					TagIDs:          []string{"tag-1"},
				},
			},
			want: true,
		},
		{
			name: "resolved document tag target",
			searchTargets: SearchTargets{
				{
					Type:                    SearchTargetTypeKnowledge,
					KnowledgeBaseID:         "kb-1",
					KnowledgeIDs:            []string{"doc-1"},
					ScopeTagIDs:             []string{"tag-1"},
					DisableRecallThresholds: true,
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasKnowledgeRetrievalScope(tt.searchTargets, tt.knowledgeBaseIDs, tt.knowledgeIDs)
			if got != tt.want {
				t.Fatalf("HasKnowledgeRetrievalScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSearchTargetRecallThresholds(t *testing.T) {
	normal := &SearchTarget{}
	vector, keyword := normal.RecallThresholds(0.5, 0.24)
	if vector != 0.5 || keyword != 0.24 {
		t.Fatalf("normal thresholds = (%v, %v), want (0.5, 0.24)", vector, keyword)
	}

	explicit := &SearchTarget{DisableRecallThresholds: true}
	vector, keyword = explicit.RecallThresholds(0.5, 0.24)
	if vector != 0 || keyword != 0 {
		t.Fatalf("explicit thresholds = (%v, %v), want (0, 0)", vector, keyword)
	}
	if !(SearchTargets{explicit}).HasRecallThresholdOverride() {
		t.Fatal("expected explicit target to advertise recall threshold override")
	}
}

func TestChatManageCloneCopiesSearchTargetScope(t *testing.T) {
	original := &ChatManage{
		PipelineRequest: PipelineRequest{
			SearchTargets: SearchTargets{
				{
					Type:                    SearchTargetTypeKnowledge,
					KnowledgeBaseID:         "kb-1",
					KnowledgeIDs:            []string{"doc-1"},
					ScopeTagIDs:             []string{"tag-1"},
					DisableRecallThresholds: true,
				},
			},
		},
	}

	cloned := original.Clone()
	if len(cloned.SearchTargets) != 1 {
		t.Fatalf("cloned search targets length = %d, want 1", len(cloned.SearchTargets))
	}
	got := cloned.SearchTargets[0]
	if !got.DisableRecallThresholds || len(got.ScopeTagIDs) != 1 || got.ScopeTagIDs[0] != "tag-1" {
		t.Fatalf("cloned target lost explicit scope: %#v", got)
	}

	got.KnowledgeIDs[0] = "changed-doc"
	got.ScopeTagIDs[0] = "changed-tag"
	if original.SearchTargets[0].KnowledgeIDs[0] != "doc-1" || original.SearchTargets[0].ScopeTagIDs[0] != "tag-1" {
		t.Fatal("Clone() did not deep-copy search target scope slices")
	}
}
