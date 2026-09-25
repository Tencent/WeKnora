package milvus

import (
	"testing"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/stretchr/testify/require"
)

func TestCollectionDimensionForBase(t *testing.T) {
	dimension, ok := collectionDimensionForBase("weknora_embeddings_1536", "weknora_embeddings")
	require.True(t, ok)
	require.Equal(t, 1536, dimension)

	_, ok = collectionDimensionForBase("weknora_embeddings_multilingual_1536", "weknora_embeddings")
	require.False(t, ok)
	_, ok = collectionDimensionForBase("weknora_embeddings", "weknora_embeddings")
	require.False(t, ok)
	_, ok = collectionDimensionForBase("weknora_embeddings_1536_backup", "weknora_embeddings")
	require.False(t, ok)

	require.True(t, matchesDimensionCollection(
		"weknora_embeddings_multilingual_1536",
		"weknora_embeddings_multilingual",
	))
	require.False(t, matchesDimensionCollection("weknora_embeddings_1536", "weknora_embeddings_multilingual"))
}

func TestParseMetricType(t *testing.T) {
	metric, err := ParseMetricType("")
	require.NoError(t, err)
	require.Equal(t, entity.MetricType(""), metric)

	metric, err = ParseMetricType(" cosine ")
	require.NoError(t, err)
	require.Equal(t, entity.COSINE, metric)

	_, err = ParseMetricType("dot")
	require.Error(t, err)
}

func TestResolveMigrationMetric(t *testing.T) {
	metric, err := resolveMigrationMetric("", entity.IP)
	require.NoError(t, err)
	require.Equal(t, entity.IP, metric)

	metric, err = resolveMigrationMetric(entity.IP, entity.IP)
	require.NoError(t, err)
	require.Equal(t, entity.IP, metric)

	_, err = resolveMigrationMetric(entity.COSINE, entity.IP)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match")

	_, err = resolveMigrationMetric("", "")
	require.Error(t, err)
}

func TestMetricTypeFromIndexParams(t *testing.T) {
	metric, err := metricTypeFromIndexParams(map[string]string{
		index.MetricTypeKey: "IP",
	})
	require.NoError(t, err)
	require.Equal(t, entity.IP, metric)

	metric, err = metricTypeFromIndexParams(map[string]string{
		index.ParamsKey: `{"metric_type":"COSINE"}`,
	})
	require.NoError(t, err)
	require.Equal(t, entity.COSINE, metric)

	_, err = metricTypeFromIndexParams(map[string]string{})
	require.Error(t, err)
}

func TestPrimaryKeyForMigratedRow(t *testing.T) {
	id, err := primaryKeyForMigratedRow(&MilvusVectorEmbeddingWithScore{
		MilvusVectorEmbedding: MilvusVectorEmbedding{ID: "pk-1"},
	}, nil, 0)
	require.NoError(t, err)
	require.Equal(t, "pk-1", id)

	ids := column.NewColumnVarChar(fieldID, []string{"from-ids"})
	id, err = primaryKeyForMigratedRow(&MilvusVectorEmbeddingWithScore{}, ids, 0)
	require.NoError(t, err)
	require.Equal(t, "from-ids", id)

	_, err = primaryKeyForMigratedRow(&MilvusVectorEmbeddingWithScore{}, nil, 3)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing primary key")
}

func TestConvertResultSetFloatVector(t *testing.T) {
	embedding := []float32{0.1, 0.2, 0.3}
	docs, _, err := convertResultSet([]client.ResultSet{{
		ResultCount: 1,
		Fields: client.DataSet{
			column.NewColumnVarChar(fieldID, []string{"pk-1"}),
			column.NewColumnVarChar(fieldContent, []string{"hello"}),
			column.NewColumnVarChar(fieldLanguage, []string{"english"}),
			column.NewColumnFloatVector(fieldEmbedding, 3, [][]float32{embedding}),
		},
	}})
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "pk-1", docs[0].ID)
	require.Equal(t, "hello", docs[0].Content)
	require.Equal(t, "english", docs[0].Language)
	require.Equal(t, embedding, docs[0].Embedding)
}

func TestConvertResultSetDoubleArray(t *testing.T) {
	docs, _, err := convertResultSet([]client.ResultSet{{
		ResultCount: 1,
		Fields: client.DataSet{
			column.NewColumnVarChar(fieldID, []string{"pk-1"}),
			column.NewColumnDoubleArray(fieldEmbedding, [][]float64{{1, 2, 3}}),
		},
	}})
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, []float32{1, 2, 3}, docs[0].Embedding)
}
