package milvus

import (
	"bytes"
	"context"
	"net"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-proto/go-api/v2/milvuspb"
	"github.com/milvus-io/milvus-proto/go-api/v2/schemapb"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// fakeMilvusService answers the three RPCs KeywordsRetrieve needs. The
// repository holds a concrete *client.Client, so an in-process Milvus endpoint
// is the only seam that exercises the real search path instead of a stub.
type fakeMilvusService struct {
	milvuspb.UnimplementedMilvusServiceServer
	collections []string
	search      func(collectionName string) (*milvuspb.SearchResults, error)
	searches    []string
}

func (s *fakeMilvusService) ShowCollections(
	_ context.Context, _ *milvuspb.ShowCollectionsRequest,
) (*milvuspb.ShowCollectionsResponse, error) {
	return &milvuspb.ShowCollectionsResponse{
		Status:          &commonpb.Status{},
		CollectionNames: s.collections,
	}, nil
}

func (s *fakeMilvusService) DescribeCollection(
	_ context.Context, request *milvuspb.DescribeCollectionRequest,
) (*milvuspb.DescribeCollectionResponse, error) {
	return &milvuspb.DescribeCollectionResponse{
		Status: &commonpb.Status{},
		Schema: &schemapb.CollectionSchema{
			Name: request.GetCollectionName(),
			Fields: []*schemapb.FieldSchema{
				{FieldID: 100, Name: fieldID, DataType: schemapb.DataType_VarChar, IsPrimaryKey: true},
				{FieldID: 101, Name: fieldContent, DataType: schemapb.DataType_VarChar},
				{FieldID: 102, Name: fieldContentSparse, DataType: schemapb.DataType_SparseFloatVector},
			},
		},
	}, nil
}

func (s *fakeMilvusService) Search(
	_ context.Context, request *milvuspb.SearchRequest,
) (*milvuspb.SearchResults, error) {
	name := request.GetCollectionName()
	s.searches = append(s.searches, name)
	return s.search(name)
}

type keywordsMilvusHarness struct {
	repo *milvusRepository
	fake *fakeMilvusService
}

func newKeywordsMilvusHarness(t *testing.T, collections []string,
	search func(collectionName string) (*milvuspb.SearchResults, error),
) *keywordsMilvusHarness {
	t.Helper()
	fake := &fakeMilvusService{collections: collections, search: search}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcSrv := grpc.NewServer()
	milvuspb.RegisterMilvusServiceServer(grpcSrv, fake)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	// DisableConn skips the Connect handshake, which this fake does not serve.
	c, err := client.New(context.Background(), &client.ClientConfig{
		Address:     lis.Addr().String(),
		DisableConn: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })

	return &keywordsMilvusHarness{
		repo: &milvusRepository{client: c, collectionBaseName: "weknora_embeddings"},
		fake: fake,
	}
}

func failedMilvusSearch() *milvuspb.SearchResults {
	return &milvuspb.SearchResults{Status: &commonpb.Status{
		ErrorCode: commonpb.ErrorCode_UnexpectedError,
		Reason:    "milvus keyword search unavailable",
	}}
}

func emptyMilvusSearch() *milvuspb.SearchResults {
	return &milvuspb.SearchResults{
		Status: &commonpb.Status{},
		Results: &schemapb.SearchResultData{
			NumQueries: 1,
			Topks:      []int64{0},
			Ids:        &schemapb.IDs{IdField: &schemapb.IDs_StrId{StrId: &schemapb.StringArray{}}},
		},
	}
}

func milvusSearchHits(chunkID, content string) *milvuspb.SearchResults {
	return &milvuspb.SearchResults{
		Status: &commonpb.Status{},
		Results: &schemapb.SearchResultData{
			NumQueries: 1,
			Topks:      []int64{1},
			Scores:     []float32{0.75},
			Ids: &schemapb.IDs{
				IdField: &schemapb.IDs_StrId{StrId: &schemapb.StringArray{Data: []string{chunkID}}},
			},
			FieldsData: []*schemapb.FieldData{
				milvusStringField(fieldContent, content),
				milvusStringField(fieldChunkID, chunkID),
			},
		},
	}
}

func milvusStringField(name, value string) *schemapb.FieldData {
	return &schemapb.FieldData{
		Type:      schemapb.DataType_VarChar,
		FieldName: name,
		Field: &schemapb.FieldData_Scalars{Scalars: &schemapb.ScalarField{
			Data: &schemapb.ScalarField_StringData{
				StringData: &schemapb.StringArray{Data: []string{value}},
			},
		}},
	}
}

func keywordsMilvusParams() types.RetrieveParams {
	return types.RetrieveParams{
		Query:            "invoice total",
		TopK:             10,
		KnowledgeBaseIDs: []string{"kb-1"},
	}
}

// captureMilvusLogs redirects the package logger for one test, so the
// "No keyword matches found" conclusion can be asserted on directly.
func captureMilvusLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })
	return &buf
}

// A batch in which every matching collection fails must reach the caller as an
// error. Returning an empty result set with a nil error is indistinguishable
// from a genuine zero-hit search, and the QA pipeline answers "no relevant
// content" on top of it (#3835).
func TestMilvusKeywordsRetrieveFailsWhenEveryMatchedCollectionFails(t *testing.T) {
	logs := captureMilvusLogs(t)
	harness := newKeywordsMilvusHarness(t,
		[]string{"weknora_embeddings_1024", "weknora_embeddings_1536", "other_collection"},
		func(string) (*milvuspb.SearchResults, error) { return failedMilvusSearch(), nil },
	)

	results, err := harness.repo.KeywordsRetrieve(context.Background(), keywordsMilvusParams())

	require.Error(t, err, "all-collections failure must surface as an error")
	require.Contains(t, err.Error(), "all 2 matched collections")
	require.Contains(t, err.Error(), "milvus keyword search unavailable")
	require.Nil(t, results, "no result set may accompany the error")
	require.Equal(t,
		[]string{"weknora_embeddings_1024", "weknora_embeddings_1536"}, harness.fake.searches,
		"only base-name collections are searched",
	)
	require.NotContains(t, logs.String(), "No keyword matches found",
		"a failed batch must not be logged as a search that found no matches")
}

// One failing collection must not fail the batch as long as another one
// answered: partial results are still results. The failure must travel with
// them though (RetrieveResult.Error) — otherwise callers treat the incomplete
// answer as a complete one (#3835).
func TestMilvusKeywordsRetrieveKeepsResultsWhenOnlySomeCollectionsFail(t *testing.T) {
	logs := captureMilvusLogs(t)
	harness := newKeywordsMilvusHarness(t,
		[]string{"weknora_embeddings_1024", "weknora_embeddings_1536"},
		func(name string) (*milvuspb.SearchResults, error) {
			if name == "weknora_embeddings_1024" {
				return failedMilvusSearch(), nil
			}
			return milvusSearchHits("chunk-1", "invoice total 42"), nil
		},
	)

	results, err := harness.repo.KeywordsRetrieve(context.Background(), keywordsMilvusParams())

	require.NoError(t, err, "a partially failed batch must not fail the caller")
	require.Len(t, results, 1)
	require.Error(t, results[0].Error,
		"a partially failed batch must mark its result set with the failure")
	require.Contains(t, results[0].Error.Error(), "1 of 2 matched collections")
	require.Contains(t, results[0].Error.Error(), "milvus keyword search unavailable")
	require.Len(t, results[0].Results, 1)
	require.Equal(t, "chunk-1", results[0].Results[0].ChunkID)
	require.Equal(t, "invoice total 42", results[0].Results[0].Content)
	require.Equal(t, 0.75, results[0].Results[0].Score)
	require.Equal(t, types.KeywordsRetrieverType, results[0].RetrieverType)
	require.NotContains(t, logs.String(), "No keyword matches found",
		"a partially failed batch must not be logged as a search that found no matches")
}

// The genuine zero-hit outcome keeps its old shape: every collection answered,
// no token matched, so an empty result with a nil error is correct and the
// conclusion log still fires.
func TestMilvusKeywordsRetrieveReportsNoMatchesWhenNothingFailed(t *testing.T) {
	logs := captureMilvusLogs(t)
	harness := newKeywordsMilvusHarness(t, []string{"weknora_embeddings_1024"},
		func(string) (*milvuspb.SearchResults, error) { return emptyMilvusSearch(), nil },
	)

	results, err := harness.repo.KeywordsRetrieve(context.Background(), keywordsMilvusParams())

	require.NoError(t, err, "a successful search without matches must not error")
	require.Len(t, results, 1)
	require.Empty(t, results[0].Results)
	require.NoError(t, results[0].Error, "a complete zero-hit search must not mark an error")
	require.Contains(t, logs.String(), "No keyword matches found",
		"a successful zero-hit search must still be logged as such")
}

// A partial failure with zero hits is still a partial failure: the result set
// stays empty but must carry the error, and the conclusion log must stay off.
func TestMilvusKeywordsRetrieveMarksPartialFailureWhenNothingHit(t *testing.T) {
	logs := captureMilvusLogs(t)
	harness := newKeywordsMilvusHarness(t,
		[]string{"weknora_embeddings_1024", "weknora_embeddings_1536"},
		func(name string) (*milvuspb.SearchResults, error) {
			if name == "weknora_embeddings_1024" {
				return failedMilvusSearch(), nil
			}
			return emptyMilvusSearch(), nil
		},
	)

	results, err := harness.repo.KeywordsRetrieve(context.Background(), keywordsMilvusParams())

	require.NoError(t, err, "a partially failed batch must not fail the caller")
	require.Len(t, results, 1)
	require.Empty(t, results[0].Results)
	require.Error(t, results[0].Error,
		"partial failure with zero hits must still be marked")
	require.NotContains(t, logs.String(), "No keyword matches found",
		"a partially failed batch must not be logged as a search that found no matches")
}

// No collection carries the repository's base name: nothing was searched at
// all, which must be distinguishable from a search that ran and hit nothing.
func TestMilvusKeywordsRetrieveWarnsWhenNoCollectionMatchesBaseName(t *testing.T) {
	logs := captureMilvusLogs(t)
	harness := newKeywordsMilvusHarness(t, []string{"other_collection"},
		func(string) (*milvuspb.SearchResults, error) { return emptyMilvusSearch(), nil },
	)

	results, err := harness.repo.KeywordsRetrieve(context.Background(), keywordsMilvusParams())

	require.NoError(t, err, "an empty collection list must not error")
	require.Len(t, results, 1)
	require.Empty(t, results[0].Results)
	require.NoError(t, results[0].Error, "no matching collection is not a partial failure")
	require.Empty(t, harness.fake.searches, "no Search may be issued without a matching collection")
	require.Contains(t, logs.String(), "No collection matched base name",
		"a search that never ran must be logged as such")
	require.NotContains(t, logs.String(), "No keyword matches found",
		"a search that never ran must not be logged as a search that found no matches")
}
