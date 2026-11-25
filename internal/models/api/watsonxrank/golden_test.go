package watsonxrank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The query and the five passages are verbatim from IBM's "Reranking
// document passages" guide, whose example reranks glossary definitions so
// the passage about agentic AI is listed first:
// https://dataplatform.cloud.ibm.com/docs/content/wsj/analyze-data/fm-api-rerank.html?context=wx&audience=wdp
// The guide publishes no response scores, so the reply below is a fixture
// shaped after the API reference (results of {index, score}) rather than a
// verbatim vendor reply; it keeps the guide's expected order, item 2 first:
// https://cloud.ibm.com/apidocs/watsonx-ai#text-rerank
const (
	guideQuery = "What is an agent-driven AI workflow?"
	guideModel = "cross-encoder/ms-marco-minilm-l-12-v2"
)

var guidePassages = []string{
	"A foundation model is a large-scale generative AI model that can be adapted to a wide range of downstream tasks.",
	"Generative AI is a class of AI algorithms that can produce various types of content including text, source code, imagery, audio, and synthetic data.",
	"Agentic AI is a generative AI flow that can decompose a prompt into multiple tasks, assign tasks to appropriate gen AI agents, and synthesize an answer without human intervention.",
	"AI ethics is a multidisciplinary field that studies how to optimize AI's beneficial impact while reducing risks and adverse outcomes. Examples of AI ethics issues are data responsibility and privacy, fairness, explainability, robustness, transparency, environmental sustainability, inclusion, moral agency, value alignment, accountability, trust, and technology misuse.",
	"AI governance is an organization's act of governing, through its corporate instructions, staff, processes and systems to direct, evaluate, monitor, and take corrective action throughout the AI lifecycle, to provide assurance that the AI system is operating as the organization intends, as its stakeholders expect, and as required by relevant regulation.",
}

const guideResponse = `{
  "model_id": "cross-encoder/ms-marco-minilm-l-12-v2",
  "model_version": "1.0.0",
  "created_at": "2024-05-31T12:00:00.000Z",
  "results": [
    {"index": 2, "score": 0.92},
    {"index": 0, "score": 0.61},
    {"index": 1, "score": 0.45},
    {"index": 4, "score": 0.12},
    {"index": 3, "score": 0.08}
  ]
}`

func newClient(t *testing.T, base string, settings api.RerankSettings) *Client {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: base, Model: guideModel, Auth: api.BearerAuth("iam-token")},
		Settings: settings,
	})
}

func TestRequestUsesTheWatsonxShape(t *testing.T) {
	c := newClient(t, "https://us-south.ml.cloud.ibm.com", api.RerankSettings{})
	body, err := c.BuildRequestBody(guideQuery, guidePassages)
	require.NoError(t, err)
	inputs := make([]any, len(guidePassages))
	for i, p := range guidePassages {
		inputs[i] = p
	}
	assert.Equal(t, map[string]any{
		"model_id": guideModel, "inputs": inputs, "query": guideQuery,
		"parameters": map[string]any{"return_options": map[string]any{"top_n": float64(5)}},
	}, body)
}

func TestProjectIDComesFromExtraBody(t *testing.T) {
	c := newClient(t, "https://us-south.ml.cloud.ibm.com", api.RerankSettings{
		ExtraBody: map[string]any{"project_id": "project-123"},
	})
	body, err := c.BuildRequestBody("q", []string{"d"})
	require.NoError(t, err)
	assert.Equal(t, "project-123", body["project_id"])
	assert.Equal(t, guideModel, body["model_id"])
}

func TestExtraBodyCannotReplaceCoreFields(t *testing.T) {
	c := newClient(t, "https://us-south.ml.cloud.ibm.com", api.RerankSettings{
		ExtraBody: map[string]any{
			"model_id": "other-model", "inputs": []string{"x"},
			"query": "other", "parameters": map[string]any{},
		},
	})
	body, err := c.BuildRequestBody("q", []string{"d"})
	require.NoError(t, err)
	assert.Equal(t, guideModel, body["model_id"])
	assert.Equal(t, []any{"d"}, body["inputs"])
	assert.Equal(t, "q", body["query"])
	assert.Equal(t, map[string]any{"return_options": map[string]any{"top_n": float64(1)}}, body["parameters"])
}

func TestDecodesGuideResponseAndKeepsOriginalTexts(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotAuth = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(guideResponse))
	}))
	defer server.Close()

	c := newClient(t, server.URL, api.RerankSettings{})
	got, err := c.Rerank(context.Background(), guideQuery, guidePassages)
	require.NoError(t, err)
	assert.Equal(t, "/ml/v1/text/rerank", gotPath)
	assert.Equal(t, "version=2024-05-31", gotQuery)
	assert.Equal(t, "Bearer iam-token", gotAuth)
	assert.Equal(t, []api.RerankResult{
		{Index: 2, Score: 0.92, Text: guidePassages[2]},
		{Index: 0, Score: 0.61, Text: guidePassages[0]},
		{Index: 1, Score: 0.45, Text: guidePassages[1]},
		{Index: 4, Score: 0.12, Text: guidePassages[4]},
		{Index: 3, Score: 0.08, Text: guidePassages[3]},
	}, got)
}

func TestFullEndpointIsNotDoubled(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"results":[{"index":0,"score":0.5}]}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL+"/ml/v1/text/rerank", api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.NoError(t, err)
	assert.Equal(t, "/ml/v1/text/rerank", path)
}

func TestEndpointVersionWinsOverDefault(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"results":[{"index":0,"score":0.5}]}`))
	}))
	defer server.Close()
	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: server.URL, Model: guideModel, Auth: api.BearerAuth("iam-token"),
			Query: map[string]string{"version": "2025-01-01"},
		},
	})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.NoError(t, err)
	assert.Equal(t, "version=2025-01-01", query)
}

func TestRejectsMalformedResults(t *testing.T) {
	for _, tc := range []struct{ name, reply, errorText string }{
		{"missing results", `{}`, "no results"},
		{"short reply", `{"results":[]}`, "returned 0 scores"},
		{"missing index", `{"results":[{"score":0.5}]}`, "missing index"},
		{"missing score", `{"results":[{"index":0}]}`, "missing index or score"},
		{"out of range", `{"results":[{"index":9,"score":0.5}]}`, "out of range"},
		{"duplicate index", `{"results":[{"index":0,"score":0.5},{"index":0,"score":0.4}]}`, "duplicate index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.reply))
			}))
			defer server.Close()
			c := newClient(t, server.URL, api.RerankSettings{})
			docs := []string{"a"}
			if tc.name == "duplicate index" {
				docs = []string{"a", "b"}
			}
			_, err := c.Rerank(context.Background(), "q", docs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errorText)
		})
	}
}

func TestSurfacesVendorError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":[{"code":"invalid_model","message":"model not found"}]}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL, api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_model")
}
