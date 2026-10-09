package reranking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/imageprep"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// textScorer scores a passage by a table keyed on its text, 0 when absent.
type textScorer struct {
	scores    map[string]float64
	documents []string
	err       error
}

func (s *textScorer) Rerank(_ context.Context, _ string, documents []string) ([]rerank.RankResult, error) {
	s.documents = append(s.documents, documents...)
	if s.err != nil {
		return nil, s.err
	}
	out := make([]rerank.RankResult, len(documents))
	for i, d := range documents {
		out[i] = rerank.RankResult{Index: i, RelevanceScore: s.scores[d]}
	}
	return out, nil
}

func (s *textScorer) GetModelName() string { return "text-rerank" }
func (s *textScorer) GetModelID() string   { return "text-rerank-id" }

// visionScorer also reads images, scoring every image imageScore.
type visionScorer struct {
	textScorer
	imageScore float64
	imageErr   error
	images     int
}

func (s *visionScorer) AcceptsImages() bool           { return true }
func (s *visionScorer) ImageLimits() imageprep.Limits { return imageprep.Limits{} }
func (s *visionScorer) RerankImages(_ context.Context, _ string, images []rerank.Image) (
	[]rerank.RankResult, error,
) {
	if s.imageErr != nil {
		return nil, s.imageErr
	}
	s.images += len(images)
	out := make([]rerank.RankResult, len(images))
	for i := range images {
		out[i] = rerank.RankResult{Index: i, RelevanceScore: s.imageScore}
	}
	return out, nil
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	return buf.Bytes()
}

// imageHit is an image_vector hit whose caption is caption and whose image
// carries ocrRunes characters of OCR text.
func imageHit(id, caption string, vectorScore float64, ocrRunes int) *types.SearchResult {
	info, _ := json.Marshal([]types.ImageInfo{{
		URL: "resource://" + id, Caption: caption, OCRText: strings.Repeat("字", ocrRunes),
	}})
	return &types.SearchResult{
		ID: id, Content: caption, ChunkType: string(types.ChunkTypeImageVector),
		ImageInfo: string(info), Score: 0.4, VectorScore: vectorScore,
	}
}

func textHit(id, content string) *types.SearchResult {
	return &types.SearchResult{ID: id, Content: content, ChunkType: string(types.ChunkTypeText), Score: 0.6}
}

func TestRerankScoresImageHitsByTheirImageWhenTheModelReadsImages(t *testing.T) {
	model := &visionScorer{
		textScorer: textScorer{scores: map[string]float64{"refund policy text": 0.8}},
		imageScore: 0.9,
	}
	in := []*types.SearchResult{
		textHit("t1", "refund policy text"),
		imageHit("img", "a red circle", 0.2, 0),
	}
	var loaded []string
	res := Rerank(context.Background(), model, "q", in, Options{
		Threshold: 0.5,
		LoadImage: func(_ context.Context, r *types.SearchResult) ([]byte, error) {
			loaded = append(loaded, r.ID)
			return pngBytes(t), nil
		},
	})
	assert.Equal(t, []string{"img"}, loaded)
	assert.Equal(t, 1, model.images)
	assert.NotContains(t, model.documents, "a red circle", "an image scored by its image is not also sent as text")
	assert.Equal(t, "img,t1", ids(res.Results), "the caption says nothing; the image decides")
	assert.Equal(t, 1, res.Diagnostics.ImagesScored)
}

func TestRerankFallsBackToTextWhenTheImageCannotBeScored(t *testing.T) {
	cases := map[string]struct {
		load     func(context.Context, *types.SearchResult) ([]byte, error)
		imageErr error
	}{
		"unreadable image": {load: func(context.Context, *types.SearchResult) ([]byte, error) {
			return nil, errors.New("gone")
		}},
		"not an image": {load: func(context.Context, *types.SearchResult) ([]byte, error) {
			return []byte("%PDF-1.7"), nil
		}},
		"image call fails": {load: func(context.Context, *types.SearchResult) ([]byte, error) {
			return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, nil
		}, imageErr: errors.New("upstream 500")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			model := &visionScorer{
				textScorer: textScorer{scores: map[string]float64{"a red circle": 0.7}},
				imageScore: 0.1, imageErr: tc.imageErr,
			}
			if tc.imageErr != nil {
				tc.load = func(context.Context, *types.SearchResult) ([]byte, error) { return pngBytes(t), nil }
			}
			res := Rerank(context.Background(), model, "q",
				[]*types.SearchResult{imageHit("img", "a red circle", 0.2, 0)},
				Options{Threshold: 0.5, LoadImage: tc.load})
			assert.Contains(t, model.documents, "a red circle", "scored by its text instead")
			assert.Equal(t, "img", ids(res.Results))
			assert.Zero(t, res.Diagnostics.ImagesScored)
		})
	}
}

func TestRerankKeepsPictorialImagesATextRerankerRejects(t *testing.T) {
	model := &textScorer{scores: map[string]float64{"refund policy text": 0.8}}
	in := []*types.SearchResult{
		textHit("t1", "refund policy text"),
		imageHit("photo-weak", "a photo", 0.30, 0),
		imageHit("photo-strong", "another photo", 0.55, 10),
		imageHit("photo-mid", "a third photo", 0.45, 0),
		imageHit("photo-below", "a fourth photo", 0.10, 0),
		// Mostly text: its OCR stands for it, so the text verdict stands.
		imageHit("screenshot", "a screenshot", 0.90, pictorialOCRRunes),
	}
	res := Rerank(context.Background(), model, "q", in, Options{Threshold: 0.5, ImageKeepScore: 0.2})

	assert.Equal(t, "t1,photo-strong,photo-mid", ids(res.Results),
		"ranked results first, then at most two pictorial images, strongest vector first")
	for _, r := range res.Results[1:] {
		assert.Equal(t, types.KeptByImageVector, r.Metadata[types.MetadataKeptBy])
	}
	assert.Empty(t, res.Results[0].Metadata[types.MetadataKeptBy])
	assert.Equal(t, 2, res.Diagnostics.ImagesKept)
	assert.Equal(t, 3, len(res.Indices))
	assert.Equal(t, 2, res.Indices[1], "indices still point into the input")
}

func TestRerankKeepsAnImageEvenWhenNoTextPasses(t *testing.T) {
	model := &textScorer{scores: map[string]float64{}}
	res := Rerank(context.Background(), model, "q", []*types.SearchResult{
		textHit("t1", "unrelated"),
		imageHit("photo", "a photo", 0.6, 0),
	}, Options{Threshold: 0.5, FallbackMinScore: DefaultFallbackMinScore, ImageKeepScore: 0.3})
	assert.Equal(t, "photo", ids(res.Results))
	assert.Equal(t, types.RerankOutcomeAllBelowThreshold, res.Diagnostics.Outcome)
}

func TestRerankKeepsNoImagesUnlessAsked(t *testing.T) {
	model := &textScorer{scores: map[string]float64{"refund policy text": 0.8}}
	in := []*types.SearchResult{textHit("t1", "refund policy text"), imageHit("photo", "a photo", 0.9, 0)}
	res := Rerank(context.Background(), model, "q", in, Options{Threshold: 0.5})
	assert.Equal(t, "t1", ids(res.Results))

	// A model that read the image already judged it; its verdict stands.
	vision := &visionScorer{textScorer: *model, imageScore: 0.1}
	res = Rerank(context.Background(), vision, "q", in, Options{
		Threshold: 0.5, ImageKeepScore: 0.2,
		LoadImage: func(context.Context, *types.SearchResult) ([]byte, error) { return pngBytes(t), nil },
	})
	assert.Equal(t, "t1", ids(res.Results))
	assert.Zero(t, res.Diagnostics.ImagesKept)
}
