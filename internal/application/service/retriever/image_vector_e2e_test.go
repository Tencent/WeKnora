package retriever

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	sqliterepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/sqlite"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/panjf2000/ants/v2"
	"github.com/stretchr/testify/require"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestMain whitelists loopback for the whole package: the fake embedding
// server lives on 127.0.0.1 and the SSRF guard parses its config once per
// process, so the setting has to land before the first embedder is built.
func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1")
	sqlite_vec.Auto()
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// A stand-in for a multimodal embedding server. Deliberately NOT a stub with
// canned vectors: for "a text query recalls an image" to mean anything the
// fake must behave like a joint text/image encoder, with text and images in
// ONE space — otherwise the test passes no matter how broken the plumbing is.
//
// The space is [red, blue, green, shared]. Text sets the axis of the colour
// word it names; images set the axis of their dominant colour, decoding the
// PNG bytes that actually travelled over the wire — so a dropped or
// substituted image would not land on the axis and would not be recalled.
// ---------------------------------------------------------------------------

var e2eConcepts = []string{"red", "blue", "green"}

// e2eEnvelope selects which wire format produced a vector. Real servers render
// `messages` input through a chat template (vLLM's Qwen3-VL-Embedding example
// prepends a system instruction and a trailing assistant turn) with no
// equivalent in the plain `input` array, so the same sentence lands in a
// different place through each.
//
// The fake models that with a dedicated ENVELOPE axis on top of the concept
// axis: `input` text and `messages` text land far apart even for the same
// concept, while `messages` text and images land together. A rotation of the
// concept axes would NOT work — it is a permutation, so every `input` concept
// collides with exactly one `messages` concept and the spaces cannot be told
// apart. Without the distinction these tests would pass with envelope routing
// completely broken, which is how this bug shipped once.
type e2eEnvelope int

const (
	// e2eEnvelopeInput is the plain `input: [string]` form (Embed/BatchEmbed).
	e2eEnvelopeInput e2eEnvelope = iota
	// e2eEnvelopeChat is the `messages: [...]` form (EmbedMultimodal).
	e2eEnvelopeChat
)

const (
	// e2eEnvelopeWeight dominates the concept axis, so crossing envelopes
	// costs far more similarity than matching the wrong concept does.
	e2eEnvelopeWeight = float32(0.8)
	// e2eSharedWeight keeps every vector non-zero (sqlite-vec rejects zero
	// vectors) and gives unrelated items a small floor similarity.
	e2eSharedWeight = float32(0.15)
)

// e2eVector builds the normalized vector for one concept in one envelope.
// Layout: [one axis per concept][one axis per envelope][shared].
func e2eVector(concept string, env e2eEnvelope) []float32 {
	v := make([]float32, len(e2eConcepts)+2+1)
	for i, c := range e2eConcepts {
		if c == concept {
			v[i] = 1
		}
	}
	v[len(e2eConcepts)+int(env)] = e2eEnvelopeWeight
	v[len(e2eConcepts)+2] = e2eSharedWeight

	var norm float32
	for _, x := range v {
		norm += x * x
	}
	norm = float32(sqrt64(float64(norm)))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}

// e2eEncode maps a concept onto a vector in the space of the envelope that
// carried it.
func e2eEncode(concept string, env e2eEnvelope) []float32 {
	return e2eVector(concept, env)
}

func sqrt64(x float64) float64 {
	// Avoid importing math just for one call in a fake: Newton's method is
	// plenty for a hand-built 4-dim vector.
	r := x
	for i := 0; i < 32; i++ {
		r = (r + x/r) / 2
	}
	return r
}

// e2eConceptFromText maps text to a concept axis.
func e2eConceptFromText(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "red") || strings.Contains(lower, "红"):
		return "red"
	case strings.Contains(lower, "blue") || strings.Contains(lower, "蓝"):
		return "blue"
	case strings.Contains(lower, "green") || strings.Contains(lower, "绿"):
		return "green"
	}
	return ""
}

// e2eConceptFromImage decodes encoded image bytes and returns the dominant
// colour's concept name.
func e2eConceptFromImage(data []byte) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	bounds := img.Bounds()
	var rSum, gSum, bSum, n int64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			rSum += int64(c.R)
			gSum += int64(c.G)
			bSum += int64(c.B)
			n++
		}
	}
	if n == 0 {
		return "", fmt.Errorf("empty image")
	}
	r, g, b := rSum/n, gSum/n, bSum/n
	switch {
	case r > g && r > b:
		return "red", nil
	case b > r && b > g:
		return "blue", nil
	case g > r && g > b:
		return "green", nil
	}
	return "", nil
}

// e2eSolidPNG encodes a solid-colour PNG. Real pixels, not a stub: the fake
// server decodes them to prove the bytes survived the trip.
func e2eSolidPNG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

type fakeVisionServer struct {
	server *httptest.Server

	mu sync.Mutex
	// textEnvelopeCalls / multimodalEnvelopeCalls let the test assert which
	// wire format each path used — the whole point of routing images through
	// the chat-style envelope.
	textEnvelopeCalls      int
	multimodalEnvelopeCall int
	// decodedImageConcepts records what the server actually saw on the wire.
	decodedImageConcepts []string
	// textViaChat counts TEXT-only inputs that arrived through the chat
	// envelope. Storing text in the chat space is what makes it retrievable
	// by a multimodal query, so this is the counter that catches the envelope
	// mismatch regression.
	textViaChat int
	// textViaInput counts text-only inputs that arrived through the plain
	// `input` array.
	textViaInput int
}

// newE2EEmbedder builds a real embedder pointed at the fake server, with a
// real goroutine pooler (ingestion goes through BatchEmbedWithPool). It goes
// through the factory rather than a provider constructor: the model name is
// what opts the embedder into the multimodal envelope, and that is the
// factory's decision.
func newE2EEmbedder(t *testing.T, url, modelID string) embedding.Embedder {
	t.Helper()
	pool, err := ants.NewPool(4)
	require.NoError(t, err)
	t.Cleanup(pool.Release)

	embedder, err := embedding.NewEmbedder(embedding.Config{
		Source:    types.ModelSourceRemote,
		Provider:  "generic",
		BaseURL:   url,
		APIKey:    "test-key",
		ModelID:   modelID,
		ModelName: "wemm-embedding-fake", // name matches the multimodal heuristic
	}, embedding.NewBatchEmbedder(pool), nil)
	require.NoError(t, err)
	return embedder
}

func newFakeVisionServer(t *testing.T) *fakeVisionServer {
	t.Helper()
	f := &fakeVisionServer{}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeVisionServer) handle(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	var vectors [][]float32

	if msgs, ok := body["messages"].([]any); ok {
		f.mu.Lock()
		f.multimodalEnvelopeCall++
		f.mu.Unlock()
		for _, raw := range msgs {
			msg, _ := raw.(map[string]any)
			parts, _ := msg["content"].([]any)
			var text string
			for _, p := range parts {
				part, _ := p.(map[string]any)
				switch part["type"] {
				case "text":
					text, _ = part["text"].(string)
				case "image_url":
					url, _ := part["image_url"].(map[string]any)["url"].(string)
					concept, err := f.decodeDataURI(url)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					f.mu.Lock()
					f.decodedImageConcepts = append(f.decodedImageConcepts, concept)
					f.mu.Unlock()
					// A real multimodal model folds the image and any
					// accompanying text into one vector; the image dominates
					// here so the test can attribute the result to the pixels.
					vectors = append(vectors, e2eEncode(concept, e2eEnvelopeChat))
					text = ""
				}
			}
			// Text-only multimodal call (this is the query path, and after the
			// envelope fix also the document path).
			if text != "" {
				f.mu.Lock()
				f.textViaChat++
				f.mu.Unlock()
				vectors = append(vectors, e2eEncode(e2eConceptFromText(text), e2eEnvelopeChat))
			}
		}
	} else if input, ok := body["input"]; ok {
		f.mu.Lock()
		f.textEnvelopeCalls++
		f.mu.Unlock()
		switch v := input.(type) {
		case string:
			f.countTextViaInput(1)
			vectors = append(vectors, e2eEncode(e2eConceptFromText(v), e2eEnvelopeInput))
		case []any:
			f.countTextViaInput(len(v))
			for _, item := range v {
				s, _ := item.(string)
				vectors = append(vectors, e2eEncode(e2eConceptFromText(s), e2eEnvelopeInput))
			}
		}
	}

	data := make([]map[string]any, 0, len(vectors))
	for i, v := range vectors {
		data = append(data, map[string]any{"embedding": v, "index": i})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (f *fakeVisionServer) countTextViaInput(n int) {
	f.mu.Lock()
	f.textViaInput += n
	f.mu.Unlock()
}

func (f *fakeVisionServer) decodeDataURI(uri string) (string, error) {
	const prefix = "base64,"
	idx := strings.Index(uri, prefix)
	if idx < 0 {
		return "", fmt.Errorf("image url is not a data URI")
	}
	raw, err := base64.StdEncoding.DecodeString(uri[idx+len(prefix):])
	if err != nil {
		return "", err
	}
	return e2eConceptFromImage(raw)
}

// e2eFlatten merges the per-retriever result buckets into one ranked list.
// A vector-only query produces exactly one bucket, but going through the same
// flattening the fusion layer uses keeps the assertions honest.
func e2eFlatten(t *testing.T, results []*types.RetrieveResult) []*types.IndexWithScore {
	t.Helper()
	var flat []*types.IndexWithScore
	for _, r := range results {
		flat = append(flat, r.Results...)
	}
	return flat
}

func (f *fakeVisionServer) snapshot() (int, int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.textEnvelopeCalls, f.multimodalEnvelopeCall, append([]string(nil), f.decodedImageConcepts...)
}

// envelopeCounts reports how many text-only inputs arrived through each wire
// format. The split is what says whether a knowledge base's text lives in the
// same space as its queries.
func (f *fakeVisionServer) envelopeCounts() (viaChat, viaInput int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.textViaChat, f.textViaInput
}

// ---------------------------------------------------------------------------

func newE2ESQLiteEngine(t *testing.T) interfaces.RetrieveEngineService {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := sqliterepo.NewSQLiteRetrieveEngineRepository(db)
	return NewKVHybridRetrieveEngine(repo, types.SQLiteRetrieverEngineType)
}

// TestTextQueryRecallsImageVectorEndToEnd is the end-to-end statement of the
// feature: a plain text question must retrieve the image chunk, through the
// real ingestion indexer, the real provider wire format, and a real sqlite-vec
// index. It does not verify recall quality (that needs a real model) — it
// verifies the plumbing that fails silently: the bytes reach the encoder, the
// query uses the same envelope, and both land in one ANN collection.
func TestTextQueryRecallsImageVectorEndToEnd(t *testing.T) {
	ctx := context.Background()
	fake := newFakeVisionServer(t)

	embedder := newE2EEmbedder(t, fake.server.URL, "model-e2e")
	require.True(t, embedding.SupportsImage(embedder))

	engine := newE2ESQLiteEngine(t)

	const kbID, knowledgeID = "kb-e2e", "knowledge-e2e"

	redPNG := e2eSolidPNG(t, color.RGBA{R: 220, G: 20, B: 20, A: 255})
	bluePNG := e2eSolidPNG(t, color.RGBA{R: 20, G: 20, B: 220, A: 255})

	imageIndex := func(id string, pngBytes []byte, url string) *types.IndexInfo {
		info := &types.IndexInfo{
			ID:              id,
			Content:         "![image](" + url + ")",
			SourceID:        id,
			SourceType:      types.ChunkSourceType,
			ChunkID:         id,
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: kbID,
			IsEnabled:       true,
			Modality:        types.EmbeddingModalityImage,
			ImageBytes:      pngBytes,
		}
		return info
	}

	indexInfos := []*types.IndexInfo{
		// Two text chunks that are topically irrelevant to the query. If the
		// image were embedded from its markdown content instead of its pixels,
		// these would be the only things with real text and the test would fail.
		{
			ID:              "chunk-text-blue",
			Content:         "一段关于蓝色天空的说明文字",
			SourceID:        "chunk-text-blue",
			SourceType:      types.ChunkSourceType,
			ChunkID:         "chunk-text-blue",
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: kbID,
			IsEnabled:       true,
		},
		{
			ID:              "chunk-text-green",
			Content:         "一段关于绿色草地的说明文字",
			SourceID:        "chunk-text-green",
			SourceType:      types.ChunkSourceType,
			ChunkID:         "chunk-text-green",
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: kbID,
			IsEnabled:       true,
		},
		imageIndex("chunk-image-red", redPNG, "https://example.test/red.png"),
		imageIndex("chunk-image-blue", bluePNG, "https://example.test/blue.png"),
	}

	require.NoError(t, engine.BatchIndex(ctx, embedder, indexInfos,
		[]types.RetrieverType{types.VectorRetrieverType}))

	// The image bytes must have arrived intact and been recognised as images.
	_, mmCalls, decoded := fake.snapshot()
	// One request per image: a chat-envelope request encodes the whole
	// `messages` array as ONE vector, so putting both images in a single
	// request would embed their concatenation and leave one slot empty.
	require.Equal(t, 2, mmCalls, "each image chunk must get its own multimodal request")
	require.ElementsMatch(t, []string{"red", "blue"}, decoded,
		"the encoder must receive the real image bytes, not a placeholder")

	// Query side: exactly what GetQueryEmbedding does when a KB has image
	// vectors enabled and the model reports image support.
	queryVec, err := embedding.EmbedMultimodal(ctx, embedder, embedding.Input{
		Text: "找出红色的汽车图片",
	})
	require.NoError(t, err)

	results, err := engine.Retrieve(ctx, types.RetrieveParams{
		Query:            "找出红色的汽车图片",
		Embedding:        queryVec,
		KnowledgeBaseIDs: []string{kbID},
		RetrieverType:    types.VectorRetrieverType,
		TopK:             4,
	})
	require.NoError(t, err)
	require.NotEmpty(t, results, "vector retrieval returned nothing")

	hits := e2eFlatten(t, results)
	require.GreaterOrEqual(t, len(hits), 2, "expected several vector hits, got %d", len(hits))

	require.Equal(t, "chunk-image-red", hits[0].ChunkID,
		"a text query about red must recall the red image first, got %q", hits[0].ChunkID)

	// The margin matters: a near-tie would mean the image vector carries no
	// signal and the ordering is an artefact.
	require.Greater(t, hits[0].Score, hits[1].Score,
		"top result must score strictly higher than the runner-up")
}

// TestTextQueryStillRecallsTextWhenImageVectorOff guards the other half of the
// contract: with the feature disabled, images are not embedded at all and the
// pipeline behaves exactly as it did before.
func TestTextQueryStillRecallsTextWhenImageVectorOff(t *testing.T) {
	ctx := context.Background()
	fake := newFakeVisionServer(t)

	embedder := newE2EEmbedder(t, fake.server.URL, "model-e2e-off")

	engine := newE2ESQLiteEngine(t)
	const kbID, knowledgeID = "kb-e2e-off", "knowledge-e2e-off"

	// Same fixture, but Modality is left at its zero value (text), which is what
	// the indexer sees when IndexingStrategy.ImageVectorEnabled is false.
	indexInfos := []*types.IndexInfo{
		{
			ID: "chunk-text-red", Content: "红色的消防车", SourceID: "chunk-text-red",
			SourceType: types.ChunkSourceType, ChunkID: "chunk-text-red",
			KnowledgeID: knowledgeID, KnowledgeBaseID: kbID, IsEnabled: true,
		},
		{
			ID: "chunk-text-blue", Content: "蓝色的天空", SourceID: "chunk-text-blue",
			SourceType: types.ChunkSourceType, ChunkID: "chunk-text-blue",
			KnowledgeID: knowledgeID, KnowledgeBaseID: kbID, IsEnabled: true,
		},
	}
	require.NoError(t, engine.BatchIndex(ctx, embedder, indexInfos,
		[]types.RetrieverType{types.VectorRetrieverType}))

	_, mmCalls, decoded := fake.snapshot()
	require.Equal(t, 0, mmCalls, "text chunks must not use the multimodal envelope")
	require.Empty(t, decoded)

	queryVec, err := embedder.Embed(ctx, "红色的消防车")
	require.NoError(t, err)

	results, err := engine.Retrieve(ctx, types.RetrieveParams{
		Query:            "红色的消防车",
		Embedding:        queryVec,
		KnowledgeBaseIDs: []string{kbID},
		RetrieverType:    types.VectorRetrieverType,
		TopK:             2,
	})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	hits := e2eFlatten(t, results)
	require.NotEmpty(t, hits)
	require.Equal(t, "chunk-text-red", hits[0].ChunkID)
}

// e2eTextOnlyFixtures builds red / blue / green text chunks for a knowledge
// base that stores image vectors. There are no images in it on purpose: the
// question these tests ask is what happens to the TEXT when a knowledge base
// goes multimodal.
func e2eTextOnlyFixtures(kbID, knowledgeID string) []*types.IndexInfo {
	newInfo := func(id, content string) *types.IndexInfo {
		return &types.IndexInfo{
			ID: id, Content: content, SourceID: id,
			SourceType: types.ChunkSourceType, ChunkID: id,
			KnowledgeID: knowledgeID, KnowledgeBaseID: kbID, IsEnabled: true,
		}
	}
	return []*types.IndexInfo{
		newInfo("chunk-text-red", "红色的消防车"),
		newInfo("chunk-text-blue", "蓝色的天空"),
		newInfo("chunk-text-green", "绿色的草地"),
	}
}

// TestTextSharesTheQueryEnvelopeWhenImageVectorOn is the end-to-end guard for
// the envelope mismatch.
//
// Enabling image vectors commits a knowledge base to the chat-template space,
// because images can only be encoded through the `messages` envelope. Its text
// chunks have to move with it: if they stay on the plain `input` array, a
// query in the chat space still returns k results, they are just noise — and
// nothing errors. That is the failure this test is here to prevent.
func TestTextSharesTheQueryEnvelopeWhenImageVectorOn(t *testing.T) {
	ctx := context.Background()
	fake := newFakeVisionServer(t)
	embedder := newE2EEmbedder(t, fake.server.URL, "model-envelope-on")
	engine := newE2ESQLiteEngine(t)
	const kbID, knowledgeID = "kb-envelope-on", "knowledge-envelope-on"

	indexInfos := e2eTextOnlyFixtures(kbID, knowledgeID)
	// Exactly what indexChunksWithEnvelope does when the KB needs image
	// vectors: every entry is stamped, text included.
	types.MarkMultimodalEnvelope(indexInfos)

	require.NoError(t, engine.BatchIndex(ctx, embedder, indexInfos,
		[]types.RetrieverType{types.VectorRetrieverType}))

	viaChat, viaInput := fake.envelopeCounts()
	require.Equal(t, 3, viaChat, "text in a multimodal KB must be encoded through the chat envelope")
	require.Zero(t, viaInput, "text must not fall back to the plain input array")

	// The query side is unconditionally multimodal once the KB stores image
	// vectors — see GetQueryEmbedding.
	queryVec, err := embedding.EmbedMultimodal(ctx, embedder, embedding.Input{Text: "红色的消防车"})
	require.NoError(t, err)

	results, err := engine.Retrieve(ctx, types.RetrieveParams{
		Query:            "红色的消防车",
		Embedding:        queryVec,
		KnowledgeBaseIDs: []string{kbID},
		RetrieverType:    types.VectorRetrieverType,
		TopK:             3,
	})
	require.NoError(t, err)
	hits := e2eFlatten(t, results)
	require.NotEmpty(t, hits)

	require.Equal(t, "chunk-text-red", hits[0].ChunkID)
	// The score is the point. Ordering alone would survive the mismatch: a
	// cross-envelope match still beats an unrelated chunk, it just lands far
	// from where it should.
	require.Greater(t, hits[0].Score, 0.99,
		"a text chunk in the chat space must sit essentially on top of the query, got %.4f", hits[0].Score)
}

// TestUnstampedTextIsUnreachableFromTheQueryEnvelope is a test about the test:
// it pins down what the mismatch costs so the numbers above mean something. If
// the fake ever stops separating the two spaces, this is what fails.
func TestUnstampedTextIsUnreachableFromTheQueryEnvelope(t *testing.T) {
	ctx := context.Background()
	fake := newFakeVisionServer(t)
	embedder := newE2EEmbedder(t, fake.server.URL, "model-envelope-off")
	engine := newE2ESQLiteEngine(t)
	const kbID, knowledgeID = "kb-envelope-off", "knowledge-envelope-off"

	indexInfos := e2eTextOnlyFixtures(kbID, knowledgeID)
	// Deliberately NOT stamped — the state a knowledge base is left in when
	// the write path forgets the envelope.
	require.NoError(t, engine.BatchIndex(ctx, embedder, indexInfos,
		[]types.RetrieverType{types.VectorRetrieverType}))

	queryVec, err := embedding.EmbedMultimodal(ctx, embedder, embedding.Input{Text: "红色的消防车"})
	require.NoError(t, err)

	results, err := engine.Retrieve(ctx, types.RetrieveParams{
		Query:            "红色的消防车",
		Embedding:        queryVec,
		KnowledgeBaseIDs: []string{kbID},
		RetrieverType:    types.VectorRetrieverType,
		TopK:             3,
	})
	require.NoError(t, err)
	hits := e2eFlatten(t, results)
	require.NotEmpty(t, hits)

	// Still ranked first — which is precisely why the bug is invisible in
	// production until someone notices that search feels worse.
	require.Equal(t, "chunk-text-red", hits[0].ChunkID)
	require.Less(t, hits[0].Score, 0.7,
		"cross-envelope text must be visibly degraded; if it scores %.4f the fake no longer "+
			"separates the two spaces", hits[0].Score)
}
