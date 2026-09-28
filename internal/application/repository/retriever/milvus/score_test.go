package milvus

import (
	"testing"

	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/stretchr/testify/require"
)

// Milvus L2 reports the squared distance of unit vectors, 2 - 2cos. Scores
// leave the driver as cosine similarity and thresholds map onto the radius.
func TestMetricConversionL2(t *testing.T) {
	t.Parallel()
	m := &milvusRepository{metricType: entity.L2}
	require.InDelta(t, 1.0, m.metricToSimilarity(0), 1e-9)
	require.InDelta(t, 0.5, m.metricToSimilarity(1), 1e-9)
	require.Greater(t, m.metricToSimilarity(0.2), m.metricToSimilarity(0.8), "nearer must score higher")
	require.InDelta(t, 1.0, m.similarityToMetric(0.5), 1e-9)
	require.InDelta(t, 0.5, m.metricToSimilarity(m.similarityToMetric(0.5)), 1e-9)
}

func TestMetricConversionIPAndCosinePassThrough(t *testing.T) {
	t.Parallel()
	for _, metric := range []entity.MetricType{entity.IP, entity.COSINE} {
		m := &milvusRepository{metricType: metric}
		require.InDelta(t, 0.73, m.metricToSimilarity(0.73), 1e-9)
		require.InDelta(t, 0.4, m.similarityToMetric(0.4), 1e-9)
	}
}
