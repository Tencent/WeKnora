package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanKnowledgeRebuild(t *testing.T) {
	tests := []struct {
		name      string
		requested []string
		full      bool
		stages    []string
		post      []string
		wantErr   bool
	}{
		{name: "omitted keeps full reparse", full: true, stages: rebuildStageOrder[:5], post: postProcessStageOrder},
		{name: "embedding only", requested: []string{"embedding"}, stages: []string{"embedding"}},
		{name: "one postprocess child", requested: []string{"journal_rank"}, stages: []string{"postprocess"}, post: []string{"journal_rank"}},
		{name: "content type only", requested: []string{"content_type"}, stages: []string{"postprocess"}, post: []string{"content_type"}},
		{name: "postprocess selects all children", requested: []string{"postprocess"}, stages: []string{"postprocess"}, post: postProcessStageOrder},
		{name: "transient stage expands to full", requested: []string{"multimodal"}, full: true, stages: rebuildStageOrder[:5], post: postProcessStageOrder},
		{name: "deduplicates and normalizes", requested: []string{" Questions ", "embedding", "questions"}, stages: []string{"embedding", "postprocess"}, post: []string{"questions"}},
		{name: "rejects unknown", requested: []string{"made_up"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := PlanKnowledgeRebuild(tt.requested)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.full, plan.Full)
			require.Equal(t, tt.stages, plan.Stages)
			require.Equal(t, tt.post, plan.PostProcessStages)
		})
	}
}
