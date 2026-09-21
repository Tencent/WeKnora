package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two SDK-backed vendors have no protocol package, so their outbound
// shape is not covered by a golden test. These pin what the SDK actually puts
// on the wire, which is what the per-client tests did before the migration.

const volcengineRerankPath = "/api/knowledge/service/rerank"

func TestVolcengineClientOutboundShape(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")

	var request struct {
		Datas []struct {
			Query   string  `json:"query"`
			Content *string `json:"content"`
		} `json:"datas"`
		RerankModel       *string `json:"rerank_model"`
		RerankInstruction *string `json:"rerank_instruction"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, volcengineRerankPath, r.URL.Path)
		// The access key identifies the caller in the signature; the secret
		// signs it and must never travel.
		assert.Contains(t, r.Header.Get("Authorization"), "AKLT-test")
		assert.NotContains(t, r.Header.Get("Authorization"), "secret-test")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":{"scores":[0.91,0.27]}}`))
	}))
	defer server.Close()

	client, err := newVolcengineClient(
		&RerankerConfig{APIKey: "AKLT-test", AppSecret: "secret-test"},
		&catalog.Resolved{BaseURL: server.URL, RemoteModel: "doubao-seed-rerank"},
	)
	require.NoError(t, err)

	got, err := client.Rerank(context.Background(), "保留对话数据吗", []string{"会保留", "不会保留"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 0, got[0].Index)
	assert.Equal(t, "会保留", got[0].Text)
	assert.InDelta(t, 0.91, got[0].Score, 1e-4)
	assert.Equal(t, 1, got[1].Index)
	assert.InDelta(t, 0.27, got[1].Score, 1e-4)

	require.NotNil(t, request.RerankModel)
	assert.Equal(t, "doubao-seed-rerank", *request.RerankModel)
	require.Len(t, request.Datas, 2)
	assert.Equal(t, "保留对话数据吗", request.Datas[0].Query)
	require.NotNil(t, request.Datas[0].Content)
	assert.Equal(t, "会保留", *request.Datas[0].Content)
	require.NotNil(t, request.RerankInstruction)
	assert.Contains(t, *request.RerankInstruction, "Document")
}

func TestVolcengineClientSurfacesAnInBodyErrorCode(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":1000030,"message":"quota exceeded","data":{}}`))
	}))
	defer server.Close()

	client, err := newVolcengineClient(
		&RerankerConfig{APIKey: "AKLT-test", AppSecret: "secret-test"},
		&catalog.Resolved{BaseURL: server.URL, RemoteModel: "doubao-seed-rerank"},
	)
	require.NoError(t, err)

	_, err = client.Rerank(context.Background(), "q", []string{"d"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota exceeded")
}

// A score list that does not line up with the documents cannot be mapped back
// onto them, so it is an error rather than a partially filled result.
func TestVolcengineClientRejectsAScoreCountMismatch(t *testing.T) {
	withRerankSSRFWhitelist(t, "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"scores":[0.5]}}`))
	}))
	defer server.Close()

	client, err := newVolcengineClient(
		&RerankerConfig{APIKey: "AKLT-test", AppSecret: "secret-test"},
		&catalog.Resolved{BaseURL: server.URL, RemoteModel: "doubao-seed-rerank"},
	)
	require.NoError(t, err)

	_, err = client.Rerank(context.Background(), "q", []string{"d0", "d1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "score count mismatch")
}

func TestVolcengineClientRequiresAKAndSK(t *testing.T) {
	for _, cfg := range []*RerankerConfig{
		{AppSecret: "sk"},
		{APIKey: "ak"},
	} {
		_, err := newVolcengineClient(cfg, &catalog.Resolved{BaseURL: "https://example.invalid"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access key and secret key are required")
	}
}

func TestLKEAPClientRequiresCredentials(t *testing.T) {
	for _, cfg := range []*RerankerConfig{
		{AppSecret: "sk"},
		{APIKey: "id"},
	} {
		_, err := newLKEAPClient(cfg, &catalog.Resolved{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "secret_id and secret_key are required")
	}
}

// The secret key has always been accepted from extra_config as well, because
// the model editor stores it there for this vendor.
func TestLKEAPClientTakesTheSecretKeyFromExtraConfig(t *testing.T) {
	client, err := newLKEAPClient(
		&RerankerConfig{APIKey: "AKIDxxx", ExtraConfig: map[string]string{"secret_key": "sk"}},
		&catalog.Resolved{RemoteModel: "lke-reranker-base"},
	)
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestLKEAPClientFallsBackToTheDefaultModel(t *testing.T) {
	client, err := newLKEAPClient(
		&RerankerConfig{APIKey: "AKIDxxx", AppSecret: "sk"},
		&catalog.Resolved{},
	)
	require.NoError(t, err)
	inner, ok := client.(*lkeapClient)
	require.True(t, ok)
	assert.Equal(t, LKEAPDefaultRerankModel, inner.modelName)
	assert.False(t, strings.Contains(inner.modelName, " "))
}
