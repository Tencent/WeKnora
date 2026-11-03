package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestProcessImageRejectsInvalidInputBeforeCallingModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "empty"},
		{name: "HTML error", body: []byte("<html><body>Access denied</body></html>")},
		{name: "JSON error", body: []byte(`{"error":"not found"}`)},
		{name: "plain text", body: []byte("not an image")},
	} {
		for _, pipeline := range []types.ImagePipelineID{types.ImagePipelineDefault, types.ImagePipelineSmartOCR} {
			t.Run(tc.name+"/"+string(pipeline), func(t *testing.T) {
				repo := &attrsChunkRepo{}
				svc := newAttrsTestService(&attrsFileService{body: tc.body}, repo)
				model := &attrsFakeVLM{reply: func(string, int) (string, error) {
					return "请提供您希望我描述的图片。", nil
				}}
				tracker := &ocrTestTracker{failed: map[string]string{}, outputs: map[string]types.JSONMap{}}
				out := types.JSONMap{}
				err := svc.processImage(context.Background(), &types.ImageMultimodalPayload{
					ImageURL: "local://1/image.png", Attempt: 1, ImagePipelineID: pipeline,
				}, model, types.VLMConfig{}, tracker, out)
				require.Error(t, err)
				require.ErrorIs(t, err, asynq.SkipRetry, "invalid input must finalize without repeated VLM attempts")
				require.Empty(t, model.calls)
				require.Empty(t, repo.created)
				require.NotEmpty(t, out["input_error"])
				require.Equal(t, "MULTIMODAL_INVALID_IMAGE", tracker.failed["multimodal.image[0]"])
			})
		}
	}
}
