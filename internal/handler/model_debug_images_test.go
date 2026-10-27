package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/imageprep"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type debugImageService struct {
	interfaces.ModelService
	row        *types.Model
	embed      *debugImageEmbedder
	similarity *similarityEmbedder
	rank       *debugImageReranker
	chat       *debugImageChat
}

func (s *debugImageService) GetModelByID(context.Context, string) (*types.Model, error) {
	return s.row, nil
}

func (s *debugImageService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	if s.similarity != nil {
		return s.similarity, nil
	}
	return s.embed, nil
}

func (s *debugImageService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return s.rank, nil
}

func (s *debugImageService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.chat, nil
}

type debugImageEmbedder struct {
	embedding.Embedder
	accepts bool
	images  []embedding.Image
	text    string
}

func (e *debugImageEmbedder) AcceptsImages() bool           { return e.accepts }
func (e *debugImageEmbedder) ImageLimits() imageprep.Limits { return imageprep.Limits{} }
func (e *debugImageEmbedder) BatchEmbedImages(_ context.Context, images []embedding.Image) ([][]float32, error) {
	e.images = images
	return [][]float32{{1, 2, 3}}, nil
}

func (e *debugImageEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.text = text
	return []float32{1, 2, 3}, nil
}

type debugImageReranker struct {
	rerank.Reranker
	accepts bool
	images  []rerank.Image
	query   string
}

func (r *debugImageReranker) AcceptsImages() bool           { return r.accepts }
func (r *debugImageReranker) ImageLimits() imageprep.Limits { return imageprep.Limits{} }
func (r *debugImageReranker) RerankImages(
	_ context.Context, query string, images []rerank.Image,
) ([]rerank.RankResult, error) {
	r.query = query
	r.images = images
	return []rerank.RankResult{{Index: 0, RelevanceScore: 0.9}}, nil
}

type debugChatBase interface{ chat.Chat }

type debugImageChat struct {
	debugChatBase
	messages []chat.Message
}

func (m *debugImageChat) ChatStream(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	m.messages = messages
	ch := make(chan types.StreamResponse, 1)
	ch <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "an image"}
	close(ch)
	return ch, nil
}

func runImageDebug(
	t *testing.T, svc *debugImageService, input string, data []byte, documents string,
) (map[string]any, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("input", input))
	require.NoError(t, w.WriteField("documents", documents))
	if data != nil {
		f, err := w.CreateFormFile("file", "test.png")
		require.NoError(t, err)
		_, err = f.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, "/models/test/debug", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: "test"}}
	(&ModelHandler{service: svc}).DebugModel(c)
	if len(c.Errors) > 0 {
		return nil, c.Errors.Last().Error()
	}
	require.Equal(t, http.StatusOK, recorder.Code)
	var result struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	return result.Data, ""
}

func debugPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return b.Bytes()
}

func TestModelDebugImageEmbedding(t *testing.T) {
	data := debugPNG(t)
	for _, tc := range []struct {
		name, input string
		file        []byte
		accepts     bool
		wantErr     string
	}{
		{"image only", "", data, true, ""},
		{"text only", "hello", nil, true, ""},
		{"unsupported image", "", data, false, "does not accept images"},
		{"invalid upload", "", []byte("not an image"), true, "not an image"},
		{"no input", "", nil, true, "input cannot be empty"},
		{"mixed input", "hello", data, true, "either text or an image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			embed := &debugImageEmbedder{accepts: tc.accepts}
			svc := &debugImageService{row: &types.Model{Type: types.ModelTypeEmbedding}, embed: embed}
			result, errText := runImageDebug(t, svc, tc.input, tc.file, "")
			if tc.wantErr != "" {
				require.Contains(t, errText, tc.wantErr)
				require.Empty(t, embed.images)
				require.Empty(t, embed.text)
				return
			}
			require.Empty(t, errText)
			require.Equal(t, true, result["ok"])
			require.Equal(t, float64(3), result["observations"].(map[string]any)["dimension"])
			if tc.file != nil {
				require.Len(t, embed.images, 1)
				require.Equal(t, data, embed.images[0].Data)
				require.Equal(t, "image/png", embed.images[0].MIMEType)
			} else {
				require.Equal(t, "hello", embed.text)
			}
		})
	}
}

// similarityEmbedder places each text on a fixed axis so the expected cosine
// is known, and records which side every call was embedded as.
type similarityEmbedder struct {
	embedding.Embedder
	accepts    bool
	imageWidth int
	queries    []string
	documents  []string
	images     int
}

var similarityAxes = map[string][]float32{
	"dog": {1, 0, 0}, "puppy": {0.9, 0.1, 0}, "car": {0, 1, 0}, "image": {0.6, 0.8, 0},
}

func (e *similarityEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if types.IsEmbedQuery(ctx) {
		e.queries = append(e.queries, text)
	}
	return similarityAxes[text], nil
}

func (e *similarityEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if !types.IsEmbedQuery(ctx) {
		e.documents = append(e.documents, texts...)
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = similarityAxes[text]
	}
	return out, nil
}

func (e *similarityEmbedder) AcceptsImages() bool           { return e.accepts }
func (e *similarityEmbedder) ImageLimits() imageprep.Limits { return imageprep.Limits{} }
func (e *similarityEmbedder) BatchEmbedImages(context.Context, []embedding.Image) ([][]float32, error) {
	e.images++
	if e.imageWidth > 0 {
		return [][]float32{make([]float32, e.imageWidth)}, nil
	}
	return [][]float32{similarityAxes["image"]}, nil
}

func runSimilarityDebug(
	t *testing.T, embed *similarityEmbedder, query, documents string, file []byte,
) (map[string]any, string) {
	t.Helper()
	svc := &debugImageService{row: &types.Model{Type: types.ModelTypeEmbedding}}
	svc.similarity = embed
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("input", query))
	require.NoError(t, w.WriteField("documents", documents))
	require.NoError(t, w.WriteField("options", `{"similarity":true}`))
	if file != nil {
		f, err := w.CreateFormFile("file", "test.png")
		require.NoError(t, err)
		_, err = f.Write(file)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, "/models/test/debug", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: "test"}}
	(&ModelHandler{service: svc}).DebugModel(c)
	if len(c.Errors) > 0 {
		return nil, c.Errors.Last().Error()
	}
	var result struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	return result.Data, ""
}

func TestModelDebugEmbeddingSimilarity(t *testing.T) {
	embed := &similarityEmbedder{accepts: true}
	result, errText := runSimilarityDebug(t, embed, "dog", `["car","puppy"]`, debugPNG(t))
	require.Empty(t, errText)
	require.Equal(t, true, result["ok"], result["error"])
	require.Equal(t, []string{"dog"}, embed.queries, "the query is embedded as a search query")
	require.Equal(t, []string{"car", "puppy"}, embed.documents, "candidates are embedded as content")
	require.Equal(t, 1, embed.images)

	results := result["raw_response"].(map[string]any)["results"].([]any)
	require.Len(t, results, 3)
	var order []string
	for _, r := range results {
		item := r.(map[string]any)
		content, _ := item["content"].(string)
		order = append(order, item["kind"].(string)+":"+content)
	}
	require.Equal(t, []string{"text:puppy", "image:", "text:car"}, order, "sorted by cosine, highest first")
	require.InDelta(t, 0.6, results[1].(map[string]any)["similarity"], 1e-6)
	require.Equal(t, float64(2), results[1].(map[string]any)["index"], "the image follows the documents")
	require.Equal(t, float64(3), result["observations"].(map[string]any)["result_count"])
}

func TestModelDebugEmbeddingSimilarityRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, query, documents string
		file                   bool
		embed                  *similarityEmbedder
		wantErr                string
	}{
		{"no query", "", `["car"]`, false, &similarityEmbedder{}, "at least one candidate"},
		{"no candidate", "dog", "", false, &similarityEmbedder{}, "at least one candidate"},
		{"image on a text model", "dog", "", true, &similarityEmbedder{}, "does not accept images"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var file []byte
			if tc.file {
				file = debugPNG(t)
			}
			_, errText := runSimilarityDebug(t, tc.embed, tc.query, tc.documents, file)
			require.Contains(t, errText, tc.wantErr)
			require.Empty(t, tc.embed.queries, "nothing is sent for a refused test")
		})
	}

	// An image vector of another width is a failed call, not a cosine of 0.
	result, errText := runSimilarityDebug(t, &similarityEmbedder{accepts: true, imageWidth: 5}, "dog", "", debugPNG(t))
	require.Empty(t, errText)
	require.Equal(t, false, result["ok"])
	require.Contains(t, result["error"], "5 dimensions, the query 3")
}

func TestModelDebugImageChat(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		vision      bool
		wantErr     bool
	}{
		{"gpt-4o", "describe", false, false},
		{"gpt-4o", "", false, false},
		{"unknown-model", "describe", true, false},
		{"unknown-model", "describe", false, true},
	} {
		t.Run(tc.name+tc.input, func(t *testing.T) {
			model := &debugImageChat{}
			svc := &debugImageService{
				row: &types.Model{
					Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Name: tc.name,
					Parameters: types.ModelParameters{Provider: "openai", SupportsVision: tc.vision},
				},
				chat: model,
			}
			result, errText := runImageDebug(t, svc, tc.input, debugPNG(t), "")
			if tc.wantErr {
				require.Contains(t, errText, "does not accept images")
				require.Empty(t, model.messages)
				return
			}
			require.Empty(t, errText)
			require.Equal(t, true, result["ok"])
			require.Len(t, model.messages, 1)
			parts := model.messages[0].MultiContent
			require.True(t, len(parts) > 0)
			require.Contains(t, parts[len(parts)-1].ImageURL.URL, "data:image/png;base64,")
			if tc.input != "" {
				require.Equal(t, tc.input, parts[0].Text)
			}
		})
	}
}

func TestModelDebugImageRerank(t *testing.T) {
	for _, tc := range []struct {
		accepts            bool
		documents, wantErr string
	}{{true, "", ""}, {false, "", "does not accept images"}, {true, `["text"]`, "either text documents or an image"}} {
		rank := &debugImageReranker{accepts: tc.accepts}
		svc := &debugImageService{row: &types.Model{Type: types.ModelTypeRerank}, rank: rank}
		result, errText := runImageDebug(t, svc, "a cat", debugPNG(t), tc.documents)
		if tc.wantErr != "" {
			require.Contains(t, errText, tc.wantErr)
			require.Empty(t, rank.images)
			continue
		}
		require.Empty(t, errText)
		require.Equal(t, true, result["ok"])
		require.Len(t, rank.images, 1)
		require.Equal(t, "a cat", rank.query)
	}
}

func TestRerankResponseIncludesImageCapabilities(t *testing.T) {
	row := &types.Model{
		Type: types.ModelTypeRerank, Source: types.ModelSourceRemote,
		Name: "nvidia/llama-nemotron-rerank-vl-1b-v2", Parameters: types.ModelParameters{Provider: "nvidia"},
	}
	response := dto.NewModelResponse(context.Background(), row)
	require.NotNil(t, response.Capabilities)
	require.Contains(t, response.Capabilities.Input, "image")
}
