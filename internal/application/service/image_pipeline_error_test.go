package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

func TestProcessImagePipelineFailureWithoutContent(t *testing.T) {
	captionErr := errors.New("caption unavailable")
	ocrErr := errors.New("OCR unavailable")
	for _, tc := range []struct {
		name       string
		pipeline   types.ImagePipelineID
		params     map[string]any
		caption    string
		ocr        string
		captionErr error
		ocrErr     error
		wantErr    error
		wantChunks int
	}{
		{
			name: "caption only fails", params: map[string]any{"enable_ocr": false},
			captionErr: captionErr, wantErr: captionErr,
		},
		{
			name: "caption fails and OCR has no text", ocr: "No text content.",
			captionErr: captionErr, wantErr: captionErr,
		},
		{
			name: "observation fails and OCR has no text", pipeline: types.ImagePipelineSmartOCR,
			ocr: "No text content.", captionErr: captionErr, wantErr: captionErr,
		},
		{
			name: "observation fails with OCR disabled", pipeline: types.ImagePipelineSmartOCR,
			params: map[string]any{"allow_ocr": false}, captionErr: captionErr, wantErr: captionErr,
		},
		{
			name: "OCR recovers from caption failure", ocr: "contract text",
			captionErr: captionErr, wantChunks: 1,
		},
		{
			name: "OCR recovers from observation failure", pipeline: types.ImagePipelineSmartOCR,
			ocr: "contract text", captionErr: captionErr, wantChunks: 1,
		},
		{
			name: "OCR failure remains fatal after caption success", caption: "a contract page",
			ocrErr: ocrErr, wantErr: ocrErr,
		},
		{
			name:       "OCR failure remains fatal after caption failure",
			captionErr: captionErr, ocrErr: ocrErr, wantErr: ocrErr,
		},
		{name: "successful no text reply", ocr: "No text content."},
		{name: "both actions succeed", caption: "a contract page", ocr: "contract text", wantChunks: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := &attrsChunkRepo{}
			svc := newAttrsTestService(&attrsFileService{body: []byte("page")}, repo)
			model := &attrsFakeVLM{reply: func(prompt string, _ int) (string, error) {
				if prompt == vlmOCRPrompt {
					return tc.ocr, tc.ocrErr
				}
				return tc.caption, tc.captionErr
			}}
			payload := &types.ImageMultimodalPayload{
				TenantID: 1, KnowledgeID: "k-1", KnowledgeBaseID: "kb-1",
				ImageURL: "local://img/0.png", ChunkID: "chunk-a",
				ImagePipelineID: tc.pipeline, ImagePipelineParams: tc.params,
			}
			out := types.JSONMap{}
			err := svc.processImage(context.Background(), payload, model, types.VLMConfig{}, noopSpanTracker{}, out)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("processImage error = %v, want %v", err, tc.wantErr)
			}
			if len(repo.created) != tc.wantChunks {
				t.Fatalf("persisted chunks = %d, want %d", len(repo.created), tc.wantChunks)
			}
			if tc.wantErr != nil {
				if _, skipped := out["skipped"]; skipped {
					t.Errorf("failed extraction reported as skipped: %v", out["skipped"])
				}
			} else if tc.wantChunks == 0 && out["skipped"] != "no_extracted_content" {
				t.Errorf("successful empty result: skipped = %v", out["skipped"])
			}
			if tc.captionErr != nil && tc.wantChunks == 1 &&
				(repo.created[0].ChunkType != types.ChunkTypeImageOCR || repo.created[0].Content != tc.ocr) {
				t.Errorf("OCR recovery did not preserve the extracted text: %+v", repo.created[0])
			}
		})
	}
}

func TestImageMultimodalHandleRetriesCaptionFailureWithoutContent(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("caption rate limited")
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	enqueuer := &orphanTaskEnqueuer{}
	svc := newAttrsTestService(&attrsFileService{body: []byte("page")}, &attrsChunkRepo{})
	svc.modelService = &fakeVLMModelService{model: &errorVLM{err: sentinel}}
	svc.kbService = &orphanKBService{kb: &types.KnowledgeBase{
		ID: "kb-1", VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm-1"},
	}}
	svc.redisClient = rdb
	svc.taskEnqueuer = enqueuer
	payload := types.ImageMultimodalPayload{
		TenantID: 1, KnowledgeID: "k-1", KnowledgeBaseID: "kb-1",
		ImageURL: "local://img/0.png", ChunkID: "chunk-a",
		ImagePipelineID:     types.ImagePipelineDefault,
		ImagePipelineParams: map[string]any{"enable_ocr": false},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	task := asynq.NewTask(types.TypeImageMultimodal, body)
	pendingKey := multimodalPendingKey(payload.KnowledgeID)
	if err := rdb.Set(context.Background(), pendingKey, 1, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Handle(types.WithTaskRetryMetadata(context.Background(), 0, 1), task); !errors.Is(err, sentinel) {
		t.Fatalf("retryable Handle error = %v, want %v", err, sentinel)
	}
	if got, err := mr.Get(pendingKey); err != nil || got != "1" {
		t.Fatalf("pending counter after retryable failure = %q, %v; want 1", got, err)
	}
	if len(enqueuer.enqueued) != 0 {
		t.Fatal("retryable failure enqueued post-processing")
	}
	if err := svc.Handle(types.WithTaskRetryMetadata(context.Background(), 1, 1), task); !errors.Is(err, sentinel) {
		t.Fatalf("final Handle error = %v, want %v", err, sentinel)
	}
	if mr.Exists(pendingKey) {
		t.Fatal("final failure did not release the pending counter")
	}
	if len(enqueuer.enqueued) != 1 || enqueuer.enqueued[0].Type() != types.TypeKnowledgePostProcess {
		t.Fatalf("post-process tasks = %#v, want one", enqueuer.enqueued)
	}
}
