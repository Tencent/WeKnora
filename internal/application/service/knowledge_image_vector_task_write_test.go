package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// Async handlers have no HTTP middleware to grant the KB write permission that
// every chunk write consumes. Forgetting it does not fail the task — the
// backfill warns per image and the switch looks flipped while nothing was
// written — so the stub below runs the real guard rather than just recording.

// guardedChunkService enforces the real KB write guard, so it cannot stay
// green while production rejects every write.
type guardedChunkService struct {
	*recordingChunkService
	kb     *types.KnowledgeBase
	denied error
}

func (s *guardedChunkService) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	if _, err := requireKBWrite(ctx, s.kb); err != nil {
		s.denied = err
		return err
	}
	return s.recordingChunkService.CreateChunks(ctx, chunks)
}

// The backfill runs inside an async task, so the handler must grant the KB
// write the chunk service consumes. Without it every image is rejected and the
// KB ends up with the switch on and zero image vectors — indistinguishable
// from "no image matched the query".
func TestProcessKBReindexVectorsGrantsChunkWrite(t *testing.T) {
	chunkRepo := &reindexChunkRepo{chunks: map[string][]*types.Chunk{
		"knowledge-1": {
			// A drawing whose OCR found nothing: no chunk of its own, recovered
			// from the markdown reference. That is the case this feature exists
			// for, and it is the one that has to survive the write guard.
			reindexTextChunk("text-1", "电路说明\n![diagram](resource://wiring)\n其余正文", true),
		},
	}}
	knowledgeRepo := &reindexKnowledgeRepo{byKB: []*types.Knowledge{{
		ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1,
		ParseStatus: types.ParseStatusCompleted,
	}}}
	kb := &types.KnowledgeBase{
		TenantID:         1,
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true},
	}
	engine := &reindexEngine{}
	chunks := &guardedChunkService{
		recordingChunkService: &recordingChunkService{},
		kb:                    kb,
	}
	reader := &backfillReader{bytes: map[string][]byte{"resource://wiring": {0x89, 0x50}}}

	svc := newReindexService(t, kb, engine, chunkRepo, knowledgeRepo)
	svc.chunkService = chunks
	svc.imageReader = reader

	payload, err := json.Marshal(types.KBReindexVectorsPayload{
		TenantID:        1,
		KnowledgeBaseID: reindexKBID,
	})
	require.NoError(t, err)

	require.NoError(t, svc.ProcessKBReindexVectors(context.Background(),
		asynq.NewTask(types.TypeKBReindexVectors, payload)))

	require.NoError(t, chunks.denied,
		"the task must grant the KB write the chunk service consumes")
	require.Len(t, chunks.created, 1, "the backfilled image_vector chunk must actually be written")
	require.Equal(t, types.ChunkTypeImageVector, chunks.created[0].ChunkType)
	require.Equal(t, []string{"resource://wiring"}, reader.asked)
}
