package qdrant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestBM25SearchFailures(t *testing.T) {
	cause := errors.New("query unavailable")
	for _, failures := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("%d failed collections", failures), func(t *testing.T) {
			queries := 0
			client := newInterceptedQdrantClient(t, func(
				_ context.Context, method string, req, reply any, _ *grpc.ClientConn,
				_ grpc.UnaryInvoker, _ ...grpc.CallOption,
			) error {
				switch request := req.(type) {
				case *qdrant.ListCollectionsRequest:
					reply.(*qdrant.ListCollectionsResponse).Collections = []*qdrant.CollectionDescription{
						{Name: "vectors_4"}, {Name: "vectors_backup_4"}, {Name: "vectors_8"},
					}
				case *qdrant.QueryPoints:
					queries++
					if queries <= failures {
						return cause
					}
					require.Equal(t, bm25VectorName, request.GetUsing())
					reply.(*qdrant.QueryResponse).Result = []*qdrant.ScoredPoint{{
						Id: qdrant.NewID("point"), Score: 4.5,
						Payload: newQdrantValueMap(map[string]any{fieldChunkID: "chunk"}),
					}}
				default:
					return fmt.Errorf("unexpected RPC %s", method)
				}
				return nil
			})
			repo := &qdrantRepository{client: client, collectionBaseName: "vectors", bm25: true}
			got, err := repo.KeywordsRetrieve(context.Background(), keywordsRetrieveParams())
			require.Equal(t, 2, queries, "backup collections must not be queried")
			if failures == 2 {
				require.ErrorIs(t, err, cause)
				require.Empty(t, got)
				return
			}
			require.NoError(t, err)
			require.Len(t, got, 2-failures)
			require.Equal(t, 4.5, got[0].Results[0].Score)
			if failures == 1 {
				require.ErrorIs(t, got[0].Error, cause)
			} else {
				require.NoError(t, got[0].Error)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = repo.KeywordsRetrieve(canceled, keywordsRetrieveParams())
			require.ErrorIs(t, err, context.Canceled)
			params := keywordsRetrieveParams()
			params.Query = "  "
			got, err = repo.KeywordsRetrieve(context.Background(), params)
			require.NoError(t, err)
			require.Empty(t, got[0].Results)
			params.TopK = -1
			_, err = repo.KeywordsRetrieve(context.Background(), params)
			require.Error(t, err)
			require.Equal(t, 2, queries, "invalid, blank or canceled queries must not search")
		})
	}
}

// Uses only fresh, randomly named collections on a local disposable server.
func TestBM25Integration(t *testing.T) {
	portText := os.Getenv("QDRANT_TEST_PORT")
	if portText == "" {
		t.Skip("set QDRANT_TEST_PORT to a disposable local Qdrant gRPC port")
	}
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	client, err := qdrant.NewClient(&qdrant.Config{Host: "127.0.0.1", Port: port})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	prefix := "bm25test_" + uuid.NewString()
	legacy := &qdrantRepository{client: client, collectionBaseName: prefix}
	ranked := &qdrantRepository{client: client, collectionBaseName: "ranked_" + prefix, bm25: true}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, name := range []string{
			legacy.getCollectionName(4), ranked.getCollectionName(4), ranked.getCollectionName(8),
		} {
			require.NoError(t, client.DeleteCollection(cleanup, name))
		}
	})
	require.NoError(t, legacy.ensureCollection(ctx, 4))
	incompatible := &qdrantRepository{client: client, collectionBaseName: prefix, bm25: true}
	require.ErrorContains(t, incompatible.ensureCollection(ctx, 4), "migrate")
	var points []*qdrant.PointStruct
	add := func(content, kb, tag string, enabled bool) string {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", len(points)+1)
		points = append(points, &qdrant.PointStruct{
			Id: qdrant.NewID(id), Vectors: qdrant.NewVectors(1, 0, 0, 0),
			Payload: createPayload(&QdrantVectorEmbedding{
				Content: content, SourceID: id, ChunkID: id, KnowledgeID: "doc", KnowledgeBaseID: kb,
				TagID: tag, IsEnabled: enabled,
			}),
		})
		return id
	}
	for range 65 {
		add("qdrant general overview unrelated installation notes", "kb", "tag", true)
	}
	want := add("qdrant migration backup restore collections", "kb", "tag", true)
	zh := add("数据库迁移备份恢复指南", "kb", "tag", true)
	ja := add("東京の図書館で本を探す", "kb", "tag", true)
	ko := add("서울 도서관: 도서 검색 안내", "kb", "tag", true)
	code := add("ZXQ-7391 firmware troubleshooting", "kb", "tag", true)
	add("qdrant migration", "other-kb", "tag", true)
	add("qdrant migration", "kb", "tag", false)
	add("qdrant migration", "kb", "other-tag", true)
	add("", "kb", "tag", true)
	wait := true
	_, err = client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: legacy.getCollectionName(4), Points: points, Wait: &wait,
	})
	require.NoError(t, err)
	params := types.RetrieveParams{
		Query: "qdrant migration", TopK: 1, KnowledgeBaseIDs: []string{"kb"}, TagIDs: []string{"tag"},
	}
	old, err := legacy.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.NotEqual(t, want, old[0].Results[0].ChunkID, "fixture must expose unranked truncation")

	copied, err := MigrateBM25(ctx, client, prefix, ranked.collectionBaseName, false)
	require.NoError(t, err)
	require.Equal(t, uint64(len(points)), copied)
	_, err = MigrateBM25(ctx, client, prefix, ranked.collectionBaseName, false)
	require.ErrorContains(t, err, "already exists")
	// Leave only the first migration page, as after interruption before page two.
	var remaining []*qdrant.PointId
	for _, point := range points[64:] {
		remaining = append(remaining, point.Id)
	}
	_, err = client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: ranked.getCollectionName(4), Wait: &wait,
		Points: qdrant.NewPointsSelectorIDs(remaining),
	})
	require.NoError(t, err)
	copied, err = MigrateBM25(ctx, client, prefix, ranked.collectionBaseName, true)
	require.NoError(t, err)
	require.Equal(t, uint64(len(points)), copied, "resuming must overwrite IDs, not duplicate points")

	for _, tc := range []struct{ query, want string }{
		{"qdrant migration", want}, {"数据库迁移", zh}, {"図書館", ja}, {"도서관", ko}, {"ZXQ-7391", code},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := params
			p.Query = tc.query
			start := time.Now()
			got, err := ranked.KeywordsRetrieve(ctx, p)
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.NoError(t, got[0].Error)
			require.Len(t, got[0].Results, 1)
			require.Equal(t, tc.want, got[0].Results[0].ChunkID)
			require.Positive(t, got[0].Results[0].Score)
			t.Logf("rank 1 correct; query took %s", time.Since(start))
		})
	}
	params.Query, params.Embedding = "ZXQ-7391", []float32{1, 0, 0, 0}
	params.KnowledgeIDs = []string{"missing"}
	got, err := ranked.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Empty(t, got[0].Results)
	params.KnowledgeIDs, params.ExcludeChunkIDs = nil, []string{code}
	got, err = ranked.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Empty(t, got[0].Results)
	params.ExcludeChunkIDs, params.ExcludeKnowledgeIDs = nil, []string{"doc"}
	got, err = ranked.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Empty(t, got[0].Results)
	params.ExcludeKnowledgeIDs = nil
	got, err = ranked.VectorRetrieve(ctx, params)
	require.NoError(t, err)
	require.NotEmpty(t, got[0].Results, "migration must preserve dense retrieval")

	// Exercise both write paths, named-vector copying, and metadata-only moves/deletes.
	index := &types.IndexInfo{
		SourceID: "new", ChunkID: "new", Content: "resumableupload", KnowledgeBaseID: "kb", KnowledgeID: "doc",
		IsEnabled: true, TagID: "tag",
	}
	embeddings := map[string]any{fieldEmbedding: map[string][]float32{"new": {1, 0, 0, 0}}}
	require.NoError(t, ranked.Save(ctx, index, embeddings))
	require.NoError(t, ranked.CopyIndices(ctx, "kb", map[string]string{"doc": "copy-doc"},
		map[string]string{"new": "copy"}, "copy-kb", 4, "doc"))
	params.Query, params.KnowledgeBaseIDs = "resumableupload", []string{"copy-kb"}
	got, err = ranked.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Equal(t, "copy", got[0].Results[0].ChunkID)
	require.NoError(t, ranked.MoveKnowledgeIndices(ctx, "copy-kb", "moved-kb", "copy-doc", nil, 4, "doc"))
	params.KnowledgeBaseIDs, params.TagIDs = []string{"moved-kb"}, nil
	got, err = ranked.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Equal(t, "copy", got[0].Results[0].ChunkID)
	require.NoError(t, ranked.DeleteByChunkIDList(ctx, []string{"copy"}, 4, "doc"))
	got, err = ranked.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Empty(t, got[0].Results)
	index.SourceID, index.ChunkID = "batch", "batch"
	embeddings[fieldEmbedding] = map[string][]float32{"batch": {1, 0, 0, 0, 0, 0, 0, 0}}
	require.NoError(t, ranked.BatchSave(ctx, []*types.IndexInfo{index}, embeddings))
	params.KnowledgeBaseIDs, params.TagIDs = []string{"kb"}, []string{"tag"}
	got, err = ranked.KeywordsRetrieve(ctx, params)
	require.NoError(t, err)
	require.Len(t, got, 2, "each dimension must retain its own ranked list")
	for _, list := range got {
		require.Len(t, list.Results, 1)
		require.NoError(t, list.Error)
	}
}
