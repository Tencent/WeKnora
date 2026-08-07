package retriever

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestCompositeRetrieveEngineSupportsMetadataFilter(t *testing.T) {
	tests := []struct {
		name  string
		infos []*engineInfo
		want  bool
	}{
		{
			name: "postgres members only",
			infos: []*engineInfo{
				{retrieveEngine: &fakeEngine{engineType: types.PostgresRetrieverEngineType}, retrieverType: []types.RetrieverType{types.VectorRetrieverType}},
				{retrieveEngine: &fakeEngine{engineType: types.PostgresRetrieverEngineType}, retrieverType: []types.RetrieverType{types.KeywordsRetrieverType}},
			},
			want: true,
		},
		{
			name: "mixed engine members",
			infos: []*engineInfo{
				{retrieveEngine: &fakeEngine{engineType: types.PostgresRetrieverEngineType}, retrieverType: []types.RetrieverType{types.VectorRetrieverType}},
				{retrieveEngine: &fakeEngine{engineType: types.ElasticsearchRetrieverEngineType}, retrieverType: []types.RetrieverType{types.KeywordsRetrieverType}},
			},
		},
		{
			name: "unknown engine member",
			infos: []*engineInfo{
				{retrieveEngine: &fakeEngine{engineType: types.RetrieverEngineType("unknown")}, retrieverType: []types.RetrieverType{types.VectorRetrieverType}},
			},
		},
		{
			name: "empty member set fails closed",
		},
		{
			name: "invalid member fails closed",
			infos: []*engineInfo{
				nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &CompositeRetrieveEngine{engineInfos: tt.infos}
			if got := engine.SupportsMetadataFilter(); got != tt.want {
				t.Fatalf("SupportsMetadataFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}
