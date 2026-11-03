package retriever

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

// The fake refuses to embed the next window before the previous one is saved.
// A distinct vector per input also catches offsets accidentally reused across windows.
type windowEmbedder struct {
	embedding.Embedder
	repo     *windowRepository
	calls    int
	failCall int
	err      error
	short    bool
}

func (e *windowEmbedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	e.calls++
	if e.failCall > 0 && e.calls >= e.failCall {
		return nil, e.err
	}
	e.repo.mu.Lock()
	saved := e.repo.saved
	e.repo.mu.Unlock()
	if saved != e.repo.expectedSaved {
		return nil, fmt.Errorf("embedded next window before saving the previous one: saved %d, want %d",
			saved, e.repo.expectedSaved)
	}
	if len(texts) > 200 {
		return nil, fmt.Errorf("unbounded embedding request: %d inputs", len(texts))
	}
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		n, err := strconv.Atoi(text)
		if err != nil {
			return nil, err
		}
		vectors[i] = []float32{float32(n)}
	}
	e.repo.expectedSaved += len(texts)
	if e.short {
		return vectors[:len(vectors)-1], nil
	}
	return vectors, nil
}

type windowRepository struct {
	saveOnlyRepository
	mu            sync.Mutex
	saved         int
	expectedSaved int
	seen          map[string]bool
	err           error
	cancel        context.CancelFunc
}

func (r *windowRepository) BatchSave(_ context.Context, rows []*types.IndexInfo, params map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	vectors := params["embedding"].(map[string][]float32)
	for _, row := range rows {
		n, _ := strconv.Atoi(row.Content)
		vector := vectors[row.SourceID]
		if len(vector) != 1 || vector[0] != float32(n) {
			return fmt.Errorf("wrong vector for %s: %v", row.SourceID, vector)
		}
		if r.seen[row.SourceID] {
			return fmt.Errorf("duplicate save: %s", row.SourceID)
		}
		r.seen[row.SourceID] = true
	}
	r.saved += len(rows)
	if r.cancel != nil {
		r.cancel()
	}
	return nil
}

func windowRows(n int) []*types.IndexInfo {
	rows := make([]*types.IndexInfo, n)
	for i := range rows {
		rows[i] = &types.IndexInfo{SourceID: fmt.Sprintf("source-%d", i), Content: strconv.Itoa(i)}
	}
	return rows
}

func TestBatchIndexEmbedsAndSavesBoundedWindows(t *testing.T) {
	for _, count := range []int{0, 1, 40, 200, 201, 437} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			repo := &windowRepository{seen: map[string]bool{}}
			model := &windowEmbedder{repo: repo}
			service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
			err := service.BatchIndex(context.Background(), model, windowRows(count),
				[]types.RetrieverType{types.VectorRetrieverType})
			if err != nil {
				t.Fatal(err)
			}
			if repo.saved != count || len(repo.seen) != count {
				t.Fatalf("saved %d rows (%d distinct), want %d", repo.saved, len(repo.seen), count)
			}
			if model.calls != (count+199)/200 {
				t.Fatalf("got %d embedding windows for %d rows", model.calls, count)
			}
		})
	}
}

func TestBatchIndexStopsAfterWindowSaveFailure(t *testing.T) {
	want := errors.New("save failed")
	repo := &windowRepository{seen: map[string]bool{}, err: want}
	model := &windowEmbedder{repo: repo}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
	err := service.BatchIndex(context.Background(), model, windowRows(437),
		[]types.RetrieverType{types.VectorRetrieverType})
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	if model.calls != 1 {
		t.Fatalf("continued embedding after save failure: %d calls", model.calls)
	}
}

func TestBatchIndexStopsBeforeNextWindowOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &windowRepository{seen: map[string]bool{}, cancel: cancel}
	model := &windowEmbedder{repo: repo}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
	err := service.BatchIndex(ctx, model, windowRows(437), []types.RetrieverType{types.VectorRetrieverType})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
	if model.calls != 1 {
		t.Fatalf("continued embedding after cancellation: %d calls", model.calls)
	}
}

func TestBatchIndexKeepsEarlierWindowsOnEmbeddingFailure(t *testing.T) {
	want := errors.New("embedding failed")
	repo := &windowRepository{seen: map[string]bool{}}
	model := &windowEmbedder{repo: repo, failCall: 2, err: want}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
	err := service.BatchIndex(context.Background(), model, windowRows(437),
		[]types.RetrieverType{types.VectorRetrieverType})
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	if repo.saved != 200 {
		t.Fatalf("saved %d rows, want first window only", repo.saved)
	}
}

func TestBatchIndexVectorsPreservesPrecomputedWindows(t *testing.T) {
	repositories := []*windowRepository{
		{seen: map[string]bool{}},
		{seen: map[string]bool{}},
	}
	engine := &CompositeRetrieveEngine{}
	for _, repo := range repositories {
		engine.engineInfos = append(engine.engineInfos, &engineInfo{
			retrieveEngine: &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo},
			retrieverType:  []types.RetrieverType{types.VectorRetrieverType},
		})
	}
	rows := windowRows(437)
	vectors := make([][]float32, len(rows))
	for i := range vectors {
		vectors[i] = []float32{float32(i)}
	}
	// A nil model proves that precomputed image vectors never call the text model.
	if err := engine.BatchIndexVectors(context.Background(), nil, rows, vectors); err != nil {
		t.Fatal(err)
	}
	for _, repo := range repositories {
		if repo.saved != len(rows) {
			t.Fatalf("saved %d rows, want %d", repo.saved, len(rows))
		}
	}
}

func TestBatchIndexRejectsIncompleteWindowVectorsBeforeSaving(t *testing.T) {
	repo := &windowRepository{seen: map[string]bool{}}
	model := &windowEmbedder{repo: repo, short: true}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}
	err := service.BatchIndex(context.Background(), model, windowRows(201),
		[]types.RetrieverType{types.VectorRetrieverType})
	if err == nil || !strings.Contains(err.Error(), "199 embeddings for 200 inputs") {
		t.Fatalf("got %v, want embedding count error", err)
	}
	if repo.saved != 0 || model.calls != 1 {
		t.Fatalf("saved %d rows with %d model calls after incomplete vectors", repo.saved, model.calls)
	}
}
