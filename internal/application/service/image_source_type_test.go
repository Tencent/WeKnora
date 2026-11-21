package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestPDFImageSourceReachesVectorWorker(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources string
		want    []string
	}{
		{
			"mixed pages", `{"images/原图.jpg":"pdf_text_layer","images/page_2.jpg":"scanned_pdf"}`,
			[]string{"pdf_text_layer", "scanned_pdf"},
		},
		{"old docreader", "", []string{"scanned_pdf", "scanned_pdf"}},
		{"invalid JSON", `{`, []string{"scanned_pdf", "scanned_pdf"}},
		{
			"invalid value", `{"images/原图.jpg":"pdf_text_layer","images/page_2.jpg":1}`,
			[]string{"scanned_pdf", "scanned_pdf"},
		},
		{
			"missing scan entry", `{"images/原图.jpg":"pdf_text_layer"}`,
			[]string{"pdf_text_layer", "scanned_pdf"},
		},
		{
			"empty entry", `{"images/原图.jpg":""}`,
			[]string{"scanned_pdf", "scanned_pdf"},
		},
		{
			"exact reference required", `{"原图.jpg":"pdf_text_layer"}`,
			[]string{"scanned_pdf", "scanned_pdf"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			knowledge, kb, images, chunks := slotReleaseFixture(2)
			knowledge.ID = "source-type-" + tc.name
			useLiteCounter(t, knowledge.ID)
			kb.IndexingStrategy.VectorEnabled = true
			kb.ImageProcessingConfig.ImageVectorEnabled = true
			images[0].OriginalRef = "images/原图.jpg"
			images[1].OriginalRef = "images/page_2.jpg"
			images[0].SourceLocators = types.SourceLocators{{Type: "pdf", Page: 1}}
			images[1].SourceLocators = types.SourceLocators{{Type: "pdf", Page: 2}}
			enqueuer := &metadataUpdateTaskEnqueuer{}
			svc := &knowledgeService{task: enqueuer}
			metadata := map[string]string{"image_source_type": "scanned_pdf", "image_source_types": tc.sources}
			require.NoError(t, svc.enqueueImageMultimodalTasks(
				context.Background(), knowledge, kb, images, chunks, metadata,
			))
			require.Len(t, enqueuer.tasks, 2)
			for index, task := range enqueuer.tasks {
				var payload types.ImageMultimodalPayload
				require.NoError(t, json.Unmarshal(task.Payload(), &payload))
				require.Equal(t, tc.want[index], payload.ImageSourceType)
				require.Equal(t, images[index].SourceLocators, payload.SourceLocators)
				worker, repo, recorder, engine := imageVectorFixture(t)
				model := &imageModel{dims: 3}
				result := worker.indexImageVector(
					context.Background(), kb, payload, testPNG(t), multimodalChunks(), model, engine,
				)
				if tc.want[index] == "scanned_pdf" {
					require.Equal(t, "skipped: scanned page", result)
					require.Empty(t, model.images)
					require.Empty(t, repo.chunks)
				} else {
					require.Equal(t, "indexed", result)
					require.Len(t, model.images, 1)
					require.Len(t, recorder.rows, 1)
				}
			}
		})
	}
}
