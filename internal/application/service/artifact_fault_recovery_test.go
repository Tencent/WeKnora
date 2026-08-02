package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recoveryDocReader struct {
	mu    sync.Mutex
	calls int
}

func (r *recoveryDocReader) Read(
	_ context.Context,
	request *types.ReadRequest,
) (*types.ReadResult, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return &types.ReadResult{
		MarkdownContent: string(request.FileContent),
		Metadata:        map[string]string{"pages": "1"},
	}, nil
}

type faultRecoveryState struct {
	chunks       map[string]*types.Chunk
	vectors      map[string][]float32
	wiki         map[string]string
	graph        map[string]string
	publishedIDs []string
	storageBytes int64
}

func newFaultRecoveryState() *faultRecoveryState {
	return &faultRecoveryState{
		chunks:  make(map[string]*types.Chunk),
		vectors: make(map[string][]float32),
		wiki:    make(map[string]string),
		graph:   make(map[string]string),
	}
}

func (s *faultRecoveryState) existingChunks() []*types.Chunk {
	result := make([]*types.Chunk, 0, len(s.chunks))
	for _, chunk := range s.chunks {
		copy := *chunk
		result = append(result, &copy)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

type faultRecoverySnapshot struct {
	chunks       map[string]string
	vectors      map[string][]float32
	wiki         map[string]string
	graph        map[string]string
	publishedIDs []string
	storageBytes int64
}

func (s *faultRecoveryState) snapshot() faultRecoverySnapshot {
	chunks := make(map[string]string, len(s.chunks))
	for id, chunk := range s.chunks {
		chunks[id] = chunk.Content
	}
	vectors := make(map[string][]float32, len(s.vectors))
	for id, vector := range s.vectors {
		vectors[id] = append([]float32(nil), vector...)
	}
	wiki := make(map[string]string, len(s.wiki))
	for id, digest := range s.wiki {
		wiki[id] = digest
	}
	graph := make(map[string]string, len(s.graph))
	for id, digest := range s.graph {
		graph[id] = digest
	}
	return faultRecoverySnapshot{
		chunks:       chunks,
		vectors:      vectors,
		wiki:         wiki,
		graph:        graph,
		publishedIDs: append([]string(nil), s.publishedIDs...),
		storageBytes: s.storageBytes,
	}
}

type faultRecoveryHarness struct {
	service  *knowledgeService
	reader   *recoveryDocReader
	embedder embedding.Embedder
}

func newFaultRecoveryHarness(t *testing.T) *faultRecoveryHarness {
	t.Helper()
	service := setupDocReaderArtifactService(t)
	provider := &pipelineCountingEmbedder{}
	return &faultRecoveryHarness{
		service: service,
		reader:  &recoveryDocReader{},
		embedder: embedding.NewArtifactCachedEmbedder(
			provider,
			service.artifactRuntime,
			embedding.ArtifactCacheConfig{
				TenantID: 1,
				Processor: artifact.ProcessorIdentity{
					ModelID:   "fault-recovery-embedding-v1",
					ModelName: "fault-recovery-embedding",
					Provider:  "counting",
				},
				Dimensions: 2,
			},
		),
	}
}

// reconcileFaultRecoveryState uses the production artifact adapters, stable
// chunk identity builder and the same non-destructive ordering as processChunks.
// The in-memory bindings make the final DB/vector/Wiki/Graph state directly
// comparable after each injected crash boundary.
func (h *faultRecoveryHarness) reconcileFaultRecoveryState(
	ctx context.Context,
	state *faultRecoveryState,
	content string,
) error {
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
	read, err := h.service.callDocReaderWithArtifact(ctx, h.reader, &types.ReadRequest{
		FileContent:  []byte(content),
		FileName:     "fault-recovery.md",
		FileType:     "md",
		ParserEngine: "counting",
	})
	if err != nil {
		return err
	}

	knowledge := &types.Knowledge{
		ID:              "fault-recovery-knowledge",
		TenantID:        1,
		KnowledgeBaseID: "fault-recovery-kb",
	}
	desired, err := buildDesiredDocumentChunks(
		knowledge,
		[]types.ParsedChunk{{
			Content:     read.MarkdownContent,
			Seq:         0,
			Start:       0,
			End:         len(read.MarkdownContent),
			ParentIndex: -1,
		}},
		nil,
		state.existingChunks(),
	)
	if err != nil {
		return err
	}

	for _, chunk := range desired.All {
		copy := *chunk
		state.chunks[chunk.ID] = &copy
	}
	if err := artifact.InjectFault(ctx, artifact.FaultAfterChunkUpsert); err != nil {
		return err
	}

	inputs := make([]string, len(desired.Text))
	for index, chunk := range desired.Text {
		inputs[index] = chunk.EmbeddingContent()
	}
	embedCtx := context.WithValue(ctx, types.EmbedDocumentContextKey, true)
	vectors, err := h.embedder.BatchEmbed(embedCtx, inputs)
	if err != nil {
		return err
	}
	for index, chunk := range desired.Text {
		state.vectors[chunk.ID] = append([]float32(nil), vectors[index]...)
	}
	if err := artifact.InjectFault(ctx, artifact.FaultAfterVectorUpsert); err != nil {
		return err
	}
	for _, chunk := range desired.Text {
		digest := artifact.SHA256Hex([]byte(chunk.Content))
		state.wiki[chunk.ID] = digest
		state.graph[chunk.ID] = digest
	}
	if err := artifact.InjectFault(ctx, artifact.FaultAfterGraphBinding); err != nil {
		return err
	}
	if err := artifact.InjectFault(ctx, artifact.FaultBeforeFence); err != nil {
		return err
	}

	state.publishedIDs = chunkIDList(desired.All)
	sort.Strings(state.publishedIDs)
	state.storageBytes = int64(len(desired.Text) * 2 * 4)
	if err := artifact.InjectFault(ctx, artifact.FaultAfterPublish); err != nil {
		return err
	}

	staleIDs := chunkIDList(desired.Stale)
	for _, id := range staleIDs {
		delete(state.vectors, id)
	}
	if len(staleIDs) > 0 {
		if err := artifact.InjectFault(ctx, artifact.FaultDuringStaleCleanup); err != nil {
			return err
		}
	}
	for _, id := range staleIDs {
		delete(state.wiki, id)
		delete(state.graph, id)
		delete(state.chunks, id)
	}
	return nil
}

func TestArtifactPipelineCrashBoundariesConvergeToCleanRun(t *testing.T) {
	points := []artifact.FaultPoint{
		artifact.FaultAfterProviderCall,
		artifact.FaultAfterArtifactPut,
		artifact.FaultAfterChunkUpsert,
		artifact.FaultAfterVectorUpsert,
		artifact.FaultAfterGraphBinding,
		artifact.FaultBeforeFence,
		artifact.FaultAfterPublish,
		artifact.FaultDuringStaleCleanup,
	}

	for _, point := range points {
		t.Run(string(point), func(t *testing.T) {
			cleanHarness := newFaultRecoveryHarness(t)
			clean := newFaultRecoveryState()
			require.NoError(t, cleanHarness.reconcileFaultRecoveryState(
				context.Background(),
				clean,
				"old generation",
			))
			require.NoError(t, cleanHarness.reconcileFaultRecoveryState(
				context.Background(),
				clean,
				"new generation",
			))

			recoveryHarness := newFaultRecoveryHarness(t)
			recovered := newFaultRecoveryState()
			require.NoError(t, recoveryHarness.reconcileFaultRecoveryState(
				context.Background(),
				recovered,
				"old generation",
			))
			injected := errors.New("injected crash boundary")
			faultCtx := artifact.WithFaultInjector(
				context.Background(),
				func(current artifact.FaultPoint) error {
					if current == point {
						return injected
					}
					return nil
				},
			)
			err := recoveryHarness.reconcileFaultRecoveryState(
				faultCtx,
				recovered,
				"new generation",
			)
			require.ErrorIs(t, err, injected)

			require.NoError(t, recoveryHarness.reconcileFaultRecoveryState(
				context.Background(),
				recovered,
				"new generation",
			))
			assert.Equal(t, clean.snapshot(), recovered.snapshot())
		})
	}
}
