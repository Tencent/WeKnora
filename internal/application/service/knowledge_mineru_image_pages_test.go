package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestMinerUImagePagesSurviveIngestion(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	// One text block followed by drawings on different pages. Empty alt text
	// must not make every drawing inherit the last searchable text block.
	const markdown = "地质说明\r\n\r\n![](images/a.png)\r\n\r\n![](./images/a.png)\r\n\r\n![](images/b.png)"
	const legacy = `[{"type":"text","text":"地质说明","page_idx":25},{"type":"image","img_path":"images/a.png","page_idx":26,"bbox":[100,200,900,800]},{"type":"image","img_path":"images/b.png","page_idx":28,"bbox":[100,200,900,800]}]`
	const middle = `{"schema":"docvortex.middle","schema_version":"2.0","pages":[{"page_idx":25,"blocks":[{"type":"text","index":0,"content":[{"type":"text","content":"地质说明"}]}]},{"page_idx":26,"blocks":[{"type":"image","index":0,"bbox":[0.1,0.2,0.9,0.8],"content":[{"type":"image_body","image_path":"images/a.png","content":""}]}]},{"page_idx":28,"blocks":[{"type":"image","index":0,"bbox":[0.1,0.2,0.9,0.8],"content":[{"type":"image_body","image_path":"images/b.png","content":""}]}]}]}`
	for _, protocol := range []string{"legacy", "v1"} {
		t.Run(protocol, func(t *testing.T) {
			var imageBytes bytes.Buffer
			require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 128, 128))))
			pngData := imageBytes.Bytes()
			var archive bytes.Buffer
			zw := zip.NewWriter(&archive)
			for name, data := range map[string][]byte{"markdown.md": []byte(markdown), "middle_json.json": []byte(middle), "images/a.png": pngData, "images/b.png": pngData} {
				w, err := zw.Create(name)
				require.NoError(t, err)
				_, err = w.Write(data)
				require.NoError(t, err)
			}
			require.NoError(t, zw.Close())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var response any
				switch r.URL.Path {
				case "/v1/health":
					if protocol == "legacy" {
						http.NotFound(w, r)
						return
					}
					response = map[string]any{"status": "ok", "version": "1.0.0"}
				case "/file_parse":
					response = map[string]any{"results": map[string]any{"report": map[string]any{"md_content": markdown, "content_list": json.RawMessage(legacy), "images": map[string]string{"images/a.png": base64.StdEncoding.EncodeToString(pngData), "images/b.png": base64.StdEncoding.EncodeToString(pngData)}}}}
				case "/v1/uploads":
					response = map[string]any{"id": "upload", "status": "completed", "file": map[string]string{"id": "source"}}
				case "/v1/parse/jobs":
					response = map[string]string{"job_id": "job", "status": "queued"}
				case "/v1/parse/jobs/job":
					response = map[string]any{"job_id": "job", "status": "completed", "files": []any{map[string]any{"name": "report.pdf", "status": "completed", "output_files": map[string]any{"zip": map[string]string{"file_id": "zip"}}}}}
				case "/v1/files/zip/content":
					w.Header().Set("Content-Type", "application/zip")
					_, _ = w.Write(archive.Bytes())
					return
				default:
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			result, err := docparser.NewMinerUReader(map[string]string{"mineru_endpoint": server.URL}).Read(context.Background(), &types.ReadRequest{FileName: "report.pdf", FileType: "pdf", FileContent: []byte("%PDF-1.7")})
			require.NoError(t, err)
			require.Empty(t, result.Error)
			parsed := result.MarkdownContent
			final, images, err := docparser.NewImageResolver().ResolveAndStore(context.Background(), result, &fakeFileService{}, 1)
			require.NoError(t, err)
			require.Len(t, images, 3)
			require.NotNil(t, buildSourceIndex(parsed, result.SourceBlocks, final, images))
			for i, wantPage := range []int{27, 27, 29} {
				require.Len(t, images[i].SourceLocators, 1)
				require.Equal(t, wantPage, images[i].SourceLocators[0].Page, "drawing must retain its own page instead of the text block's page")
				require.Equal(t, []float64{.1, .2, .9, .8}, images[i].SourceLocators[0].BBox)
				require.Equal(t, "exact", images[i].SourceLocators[0].Mapping)
			}
			// The queued caption/OCR payload is the source of gallery chunk locators.
			knowledge, kb, _, chunks := slotReleaseFixture(3)
			useLiteCounter(t, knowledge.ID)
			enqueuer := &metadataUpdateTaskEnqueuer{}
			svc := &knowledgeService{task: enqueuer}
			require.NoError(t, svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil))
			require.Len(t, enqueuer.tasks, 3)
			for i, task := range enqueuer.tasks {
				var payload types.ImageMultimodalPayload
				require.NoError(t, json.Unmarshal(task.Payload(), &payload))
				require.Equal(t, images[i].SourceLocators, payload.SourceLocators)
			}
		})
	}
}
