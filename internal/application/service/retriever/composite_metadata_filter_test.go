package retriever

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestCompositeRetrieveEngineSupportsMetadataFilter(t *testing.T) {
	var typedNilEngine *fakeEngine
	tests := []struct {
		name   string
		engine *CompositeRetrieveEngine
		want   bool
	}{
		{
			name: "postgres members only",
			engine: &CompositeRetrieveEngine{engineInfos: []*engineInfo{
				{
					retrieveEngine: &fakeEngine{engineType: types.PostgresRetrieverEngineType},
					retrieverType:  []types.RetrieverType{types.VectorRetrieverType},
				},
				{
					retrieveEngine: &fakeEngine{engineType: types.PostgresRetrieverEngineType},
					retrieverType:  []types.RetrieverType{types.KeywordsRetrieverType},
				},
			}},
			want: true,
		},
		{
			name: "nil composite fails closed",
		},
		{
			name:   "empty member set fails closed",
			engine: &CompositeRetrieveEngine{},
		},
		{
			name:   "nil engine info fails closed",
			engine: &CompositeRetrieveEngine{engineInfos: []*engineInfo{nil}},
		},
		{
			name: "nil retrieve engine fails closed",
			engine: &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
				retrieverType: []types.RetrieverType{types.VectorRetrieverType},
			}}},
		},
		{
			name: "empty retriever types fail closed",
			engine: &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
				retrieveEngine: &fakeEngine{engineType: types.PostgresRetrieverEngineType},
			}}},
		},
		{
			name: "empty string retriever type fails closed",
			engine: &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
				retrieveEngine: &fakeEngine{engineType: types.PostgresRetrieverEngineType},
				retrieverType:  []types.RetrieverType{""},
			}}},
		},
		{
			name: "unknown nonempty retriever type fails closed",
			engine: &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
				retrieveEngine: &fakeEngine{engineType: types.PostgresRetrieverEngineType},
				retrieverType:  []types.RetrieverType{"not-a-postgres-retriever"},
			}}},
		},
		{
			name: "mixed valid and invalid retriever types fail closed",
			engine: &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
				retrieveEngine: &fakeEngine{engineType: types.PostgresRetrieverEngineType},
				retrieverType:  []types.RetrieverType{types.VectorRetrieverType, ""},
			}}},
		},
		{
			name: "typed nil retrieve engine fails closed",
			engine: &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
				retrieveEngine: typedNilEngine,
				retrieverType:  []types.RetrieverType{types.VectorRetrieverType},
			}}},
		},
		{
			name: "mixed engine members",
			engine: &CompositeRetrieveEngine{engineInfos: []*engineInfo{
				{
					retrieveEngine: &fakeEngine{engineType: types.PostgresRetrieverEngineType},
					retrieverType:  []types.RetrieverType{types.VectorRetrieverType},
				},
				{
					retrieveEngine: &fakeEngine{engineType: types.ElasticsearchRetrieverEngineType},
					retrieverType:  []types.RetrieverType{types.KeywordsRetrieverType},
				},
			}},
		},
		{
			name: "unknown engine member",
			engine: &CompositeRetrieveEngine{engineInfos: []*engineInfo{
				{
					retrieveEngine: &fakeEngine{engineType: types.RetrieverEngineType("unknown")},
					retrieverType:  []types.RetrieverType{types.VectorRetrieverType},
				},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panicked := false
			got := false
			func() {
				defer func() {
					panicked = recover() != nil
				}()
				got = tt.engine.SupportsMetadataFilter()
			}()
			if panicked {
				t.Fatal("SupportsMetadataFilter() panicked")
			}
			if got != tt.want {
				t.Fatalf("SupportsMetadataFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}
