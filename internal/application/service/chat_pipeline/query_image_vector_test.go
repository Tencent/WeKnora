package chatpipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// textOnlyKBService answers text queries and has no image capability; the
// imageQueryEmbedder assertion must fail on it.
type textOnlyKBService struct {
	interfaces.KnowledgeBaseService
	textQueries int
}

func (s *textOnlyKBService) GetQueryEmbedding(
	context.Context, string, string,
) ([]float32, error) {
	s.textQueries++
	return []float32{1, 2, 3}, nil
}

// imageCapableKBService also embeds query images, recording which path ran.
type imageCapableKBService struct {
	interfaces.KnowledgeBaseService
	textQueries  int
	imageQueries int
	lastImage    []byte
	imageErr     error
}

func (s *imageCapableKBService) GetQueryEmbedding(
	context.Context, string, string,
) ([]float32, error) {
	s.textQueries++
	return []float32{1, 2, 3}, nil
}

func (s *imageCapableKBService) GetQueryImageEmbedding(
	_ context.Context, _ string, img []byte,
) ([]float32, error) {
	s.imageQueries++
	s.lastImage = img
	if s.imageErr != nil {
		return nil, s.imageErr
	}
	return []float32{7, 8, 9}, nil
}

// A text query must keep using the text embedding path.
func TestQueryVectorUsesTextPathWithoutImage(t *testing.T) {
	svc := &imageCapableKBService{}
	plugin := &PluginSearch{knowledgeBaseService: svc}

	vec, err := plugin.queryVector(context.Background(), "kb-1", "turbine drawing", nil)
	if err != nil {
		t.Fatalf("queryVector: %v", err)
	}
	if svc.textQueries != 1 || svc.imageQueries != 0 {
		t.Fatalf("text=%d image=%d, want 1/0", svc.textQueries, svc.imageQueries)
	}
	if len(vec) != 3 {
		t.Fatalf("vector = %v, want the text embedding", vec)
	}
}

// A query image must be embedded by the image path, not by the text one.
func TestQueryVectorUsesImagePathWhenImageGiven(t *testing.T) {
	svc := &imageCapableKBService{}
	plugin := &PluginSearch{knowledgeBaseService: svc}
	img := []byte{0x89, 'P', 'N', 'G'}

	vec, err := plugin.queryVector(context.Background(), "kb-1", "turbine drawing", img)
	if err != nil {
		t.Fatalf("queryVector: %v", err)
	}
	if svc.imageQueries != 1 || svc.textQueries != 0 {
		t.Fatalf("image=%d text=%d, want 1/0", svc.imageQueries, svc.textQueries)
	}
	if string(svc.lastImage) != string(img) {
		t.Fatalf("image bytes were not forwarded to the embedder")
	}
	if len(vec) != 3 || vec[0] != 7 {
		t.Fatalf("vector = %v, want the image embedding", vec)
	}
}

// A service without image capability must fail loudly, never fall back to a
// text query: the caller asked to match a picture.
func TestQueryVectorRejectsImageWithoutCapability(t *testing.T) {
	svc := &textOnlyKBService{}
	plugin := &PluginSearch{knowledgeBaseService: svc}

	_, err := plugin.queryVector(context.Background(), "kb-1", "turbine drawing", []byte{0x89, 'P'})
	if err == nil {
		t.Fatal("expected an error when the service cannot embed images")
	}
	if !strings.Contains(err.Error(), "cannot embed a query image") {
		t.Fatalf("error = %v, want it to explain the missing capability", err)
	}
	if svc.textQueries != 0 {
		t.Fatalf("text fallback ran %d times; an image query must not silently become text", svc.textQueries)
	}
}

// An embedding failure must surface, not degrade to text.
func TestQueryVectorPropagatesImageEmbeddingFailure(t *testing.T) {
	rootCause := errors.New("image endpoint unavailable")
	svc := &imageCapableKBService{imageErr: rootCause}
	plugin := &PluginSearch{knowledgeBaseService: svc}

	_, err := plugin.queryVector(context.Background(), "kb-1", "", []byte{0x89, 'P'})
	if !errors.Is(err, rootCause) {
		t.Fatalf("error = %v, want it to wrap the embedding failure", err)
	}
	if svc.textQueries != 0 {
		t.Fatalf("text fallback ran %d times", svc.textQueries)
	}
}

// The chat pipeline must be able to carry a query image through to search.
func TestPipelineRequestCarriesQueryImage(t *testing.T) {
	img := []byte{0x89, 'P', 'N', 'G'}
	cm := &types.ChatManage{PipelineRequest: types.PipelineRequest{QueryImage: img}}
	if string(cm.QueryImage) != string(img) {
		t.Fatal("PipelineRequest does not carry the query image")
	}
}

// imageQueryStubKBService resolves both targets into one model group and
// fails every image embedding with the given error.
type imageQueryStubKBService struct {
	interfaces.KnowledgeBaseService
	imageErr error
}

func (s *imageQueryStubKBService) GetKnowledgeBasesByIDsOnly(
	context.Context, []string,
) ([]*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *imageQueryStubKBService) ResolveEmbeddingModelKeys(
	context.Context, []*types.KnowledgeBase,
) map[string]string {
	return map[string]string{"kb-a": "model-1", "kb-b": "model-1"}
}

func (s *imageQueryStubKBService) GetQueryImageEmbedding(
	_ context.Context, _ string, _ []byte,
) ([]float32, error) {
	return nil, s.imageErr
}

// A partial image search must say which knowledge bases it dropped: hits from
// the surviving targets look like a complete answer otherwise.
func TestSearchByTargetsRecordsSkippedTargetsWhenImageEmbeddingFails(t *testing.T) {
	svc := &imageQueryStubKBService{imageErr: errors.New("image endpoint unavailable")}
	plugin := &PluginSearch{knowledgeBaseService: svc}

	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			QueryImage: []byte{0x89, 'P', 'N', 'G'},
			SearchTargets: []*types.SearchTarget{
				{KnowledgeBaseID: "kb-a", Type: types.SearchTargetTypeKnowledgeBase},
				{KnowledgeBaseID: "kb-b", Type: types.SearchTargetTypeKnowledgeBase},
			},
		},
		PipelineState: types.PipelineState{RewriteQuery: "diagram"},
	}

	_, err := plugin.searchByTargets(context.Background(), cm)
	if err == nil {
		t.Fatal("expected the embedding failure to surface")
	}
	diag := cm.SearchDiagnostics
	if diag == nil {
		t.Fatal("the skipped targets were not recorded")
	}
	if len(diag.SkippedTargets) != 2 {
		t.Fatalf("skipped = %v, want both targets", diag.SkippedTargets)
	}
	got := map[string]bool{}
	for _, sk := range diag.SkippedTargets {
		got[sk.KnowledgeBaseID] = true
		if !strings.Contains(sk.Reason, "image endpoint unavailable") {
			t.Fatalf("reason = %q, want the embedding failure", sk.Reason)
		}
	}
	if !got["kb-a"] || !got["kb-b"] {
		t.Fatalf("skipped = %v, want kb-a and kb-b", got)
	}
}
