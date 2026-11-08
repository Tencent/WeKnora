package providers_test

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api/ocigenai"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Synthetic Japanese passages; the query matches the second one.
var ociLiveDocs = []string{
	"社内の経費精算は、月末までに領収書を添付して申請してください。",
	"有給休暇は入社6か月後に10日付与され、申請は3営業日前までに行います。",
	"会議室の予約は社内ポータルから行い、使用後は机と椅子を元に戻します。",
	"在宅勤務の日は、始業時と終業時にチャットで上長へ連絡してください。",
}

const ociLiveQuery = "有給休暇はいつから何日もらえますか？"

// TestOCIGenAILive calls a real OCI Generative AI endpoint with an API
// signing key. Skipped unless OCI_GENAI_TEST_PRIVATE_KEY_FILE is set, e.g.
//
//	OCI_GENAI_TEST_ENDPOINT=https://inference.generativeai.us-chicago-1.oci.oraclecloud.com \
//	OCI_GENAI_TEST_COMPARTMENT=ocid1.compartment.oc1.. OCI_GENAI_TEST_TENANCY=ocid1.tenancy.oc1.. \
//	OCI_GENAI_TEST_USER=ocid1.user.oc1.. OCI_GENAI_TEST_FINGERPRINT=12:34:... \
//	OCI_GENAI_TEST_PRIVATE_KEY_FILE=~/.oci/oci_api_key.pem [OCI_GENAI_TEST_RERANK_MODEL=...] \
//	go test -run TestOCIGenAILive ./internal/models/providers/
func TestOCIGenAILive(t *testing.T) {
	keyFile := os.Getenv("OCI_GENAI_TEST_PRIVATE_KEY_FILE")
	if keyFile == "" {
		t.Skip("OCI_GENAI_TEST_PRIVATE_KEY_FILE not set")
	}
	key, err := os.ReadFile(keyFile)
	require.NoError(t, err)
	baseURL := os.Getenv("OCI_GENAI_TEST_ENDPOINT")
	extra := map[string]string{
		ocigenai.KeyAuthMode:    ocigenai.AuthAPIKey,
		ocigenai.KeyCompartment: os.Getenv("OCI_GENAI_TEST_COMPARTMENT"),
		ocigenai.KeyTenancy:     os.Getenv("OCI_GENAI_TEST_TENANCY"),
		ocigenai.KeyUser:        os.Getenv("OCI_GENAI_TEST_USER"),
		ocigenai.KeyFingerprint: os.Getenv("OCI_GENAI_TEST_FINGERPRINT"),
	}
	rerankModel := os.Getenv("OCI_GENAI_TEST_RERANK_MODEL")
	if rerankModel == "" {
		rerankModel = "cohere.rerank-v4.0-fast"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	t.Run("embed", func(t *testing.T) {
		e, err := embedding.NewEmbedder(embedding.Config{
			Source: types.ModelSourceRemote, BaseURL: baseURL, ModelName: "cohere.embed-v4.0",
			Provider: providers.OCIGenAIID, Dimensions: 1536, ExtraConfig: extra, AppSecret: string(key),
		}, nil, nil)
		require.NoError(t, err)
		docs, err := e.BatchEmbed(ctx, ociLiveDocs)
		require.NoError(t, err)
		query, err := e.BatchEmbed(types.WithEmbedQuery(ctx), []string{ociLiveQuery})
		require.NoError(t, err)
		require.Len(t, query[0], 1536)
		best, bestScore := -1, -2.0
		for i, d := range docs {
			require.Len(t, d, 1536)
			if s := cosine(query[0], d); s > bestScore {
				best, bestScore = i, s
			}
		}
		t.Logf("best document %d (cosine %.3f)", best, bestScore)
		require.Equal(t, 1, best)
	})

	t.Run("rerank", func(t *testing.T) {
		r, err := rerank.NewReranker(&rerank.RerankerConfig{
			Source: types.ModelSourceRemote, BaseURL: baseURL, ModelName: rerankModel,
			Provider: providers.OCIGenAIID, ExtraConfig: extra, AppSecret: string(key),
		})
		require.NoError(t, err)
		results, err := r.Rerank(ctx, ociLiveQuery, ociLiveDocs)
		require.NoError(t, err)
		require.Len(t, results, len(ociLiveDocs))
		t.Logf("top document %d (score %.3f)", results[0].Index, results[0].RelevanceScore)
		require.Equal(t, 1, results[0].Index)
		require.Greater(t, results[0].RelevanceScore, results[1].RelevanceScore)
	})
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
