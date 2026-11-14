package chatpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type scopedChatLookup struct {
	id     string
	tenant uint64
}
type scopedChatModels struct {
	interfaces.ModelService
	calls []scopedChatLookup
	model chat.Chat
}

func (s *scopedChatModels) GetChatModel(ctx context.Context, id string) (chat.Chat, error) {
	tenant := types.MustTenantIDFromContext(ctx)
	s.calls = append(s.calls, scopedChatLookup{id, tenant})
	// The source KB's model is private; dedicated models belong to the caller.
	if (id == "owner" && tenant == 1) || ((id == "rewrite" || id == "vision") && tenant == 2) {
		return s.model, nil
	}
	return nil, errors.New("model not found")
}

func TestSharedChatModelPipelineScope(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2))
	caller := types.CallerFromContext(ctx)
	for _, tc := range []struct {
		name, rewrite, vlm                    string
		hasImages, supportsVision, wantImages bool
		want                                  []scopedChatLookup
	}{
		{name: "default", want: []scopedChatLookup{{"owner", 1}}},
		{
			name: "owner vision", hasImages: true, supportsVision: true, wantImages: true,
			want: []scopedChatLookup{{"owner", 1}},
		},
		{name: "dedicated rewrite", rewrite: "rewrite", want: []scopedChatLookup{{"rewrite", 2}}},
		{
			name: "failed rewrite falls back to owner", rewrite: "missing",
			want: []scopedChatLookup{{"missing", 2}, {"owner", 1}},
		},
		{
			name: "same ID override still uses caller scope first", rewrite: "owner",
			want: []scopedChatLookup{{"owner", 2}, {"owner", 1}},
		},
		{
			name: "dedicated vision", hasImages: true, vlm: "vision", wantImages: true,
			want: []scopedChatLookup{{"vision", 2}},
		},
		{
			name: "failed vision falls back to owner", hasImages: true, vlm: "missing",
			want: []scopedChatLookup{{"missing", 2}, {"owner", 1}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &scopedChatModels{model: &openStreamChat{}}
			cm := &types.ChatManage{PipelineRequest: types.PipelineRequest{
				ChatModelID: "owner", ChatModelTenantID: 1, QueryUnderstandModelID: tc.rewrite,
				VLMModelID: tc.vlm, ChatModelSupportsVision: tc.supportsVision, TenantID: 2,
			}}
			// Parallel retrieval clones must retain the selected model's owner.
			cm = cm.Clone()
			model, images := (&PluginQueryUnderstand{modelService: svc}).selectModel(ctx, cm, tc.hasImages)
			require.Same(t, svc.model, model)
			require.Equal(t, tc.wantImages, images)
			require.Equal(t, tc.want, svc.calls)
			svc.calls = nil
			model, _, err := prepareChatModel(ctx, svc, cm)
			require.NoError(t, err)
			require.Same(t, svc.model, model)
			require.Equal(t, []scopedChatLookup{{"owner", 1}}, svc.calls)
			require.Equal(t, uint64(2), cm.TenantID)
			require.Equal(t, uint64(2), types.MustTenantIDFromContext(ctx))
			require.Equal(t, caller, types.CallerFromContext(cm.ChatModelContext(ctx)))
		})
	}
}
