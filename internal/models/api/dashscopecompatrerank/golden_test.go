package dashscopecompatrerank

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const endpointURL = "https://dashscope.aliyuncs.com/compatible-api/v1/reranks"

// recordedResponse is the reply of a real call to
// https://dashscope.aliyuncs.com/compatible-api/v1/reranks with the request
// body this package builds, kept verbatim. It is the shape the vendor's own
// reference documents for qwen3-rerank: results at the top level, no output
// wrapper, and no document text in any result.
// https://help.aliyun.com/zh/model-studio/text-rerank-api
const recordedResponse = `{"object":"list","results":[` +
	`{"index":2,"relevance_score":0.8464465276226582},` +
	`{"index":0,"relevance_score":0.798763089207101},` +
	`{"index":1,"relevance_score":0.32370468862378365}],` +
	`"model":"qwen3-rerank","usage":{"total_tokens":98}}`

func newClient(t *testing.T, url string, settings api.RerankSettings) *Client {
	t.Helper()
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: url, Model: "qwen3-rerank", Auth: api.BearerAuth("k")},
		Settings: settings,
	})
}

func TestRequestBodyMatchesTheDocumentedSchema(t *testing.T) {
	c := newClient(t, endpointURL, api.RerankSettings{})
	body, err := c.BuildRequestBody("谁有 CCSK 认证", []string{"张三持有 CCSK 证书", "李四持有 CISP 证书", "王五持有 CCSK 与 CISA 证书"})
	require.NoError(t, err)

	// Flat: the native dialect's input and parameters wrappers must not appear.
	assert.Equal(t, map[string]any{
		"model":     "qwen3-rerank",
		"query":     "谁有 CCSK 认证",
		"documents": []any{"张三持有 CCSK 证书", "李四持有 CISP 证书", "王五持有 CCSK 与 CISA 证书"},
		"top_n":     float64(3),
	}, body)
	assert.NotContains(t, body, "input")
	assert.NotContains(t, body, "parameters")
}

// top_n is sent as the document count rather than left to the default, which
// is every document: asking for exactly what was sent leaves nothing to the
// default and matches the vendor's request example.
func TestTopNAlwaysCarriesTheDocumentCount(t *testing.T) {
	c := newClient(t, endpointURL, api.RerankSettings{})
	body, err := c.BuildRequestBody("q", []string{"a", "b", "c", "d"})
	require.NoError(t, err)
	assert.Equal(t, float64(4), body["top_n"])
}

// The vendor declares return_documents at vendor level for its native rerank
// models, and this entry inherits that declaration. The field is not
// documented for qwen3-rerank and the documented reply carries no document
// text, so it must never reach the wire, whatever the settings say.
func TestReturnDocumentsIsNeverSent(t *testing.T) {
	c := newClient(t, endpointURL, api.RerankSettings{SendReturnDocs: true})
	body, err := c.BuildRequestBody("q", []string{"d"})
	require.NoError(t, err)
	assert.NotContains(t, body, "return_documents")

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "return_documents")
}

// The body goes out as the merged object, so its keys are marshalled in the
// order encoding/json sorts map keys rather than the struct's field order.
// This pins the bytes a real request carries.
func TestOutboundBodyIsByteForByteTheGoldenJSON(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var sent []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(recordedResponse))
	}))
	defer server.Close()

	c := newClient(t, server.URL+defaultPath, api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "谁有 CCSK 认证",
		[]string{"张三持有 CCSK 证书", "李四持有 CISP 证书", "王五持有 CCSK 与 CISA 证书"})
	require.NoError(t, err)

	assert.Equal(t,
		`{"documents":["张三持有 CCSK 证书","李四持有 CISP 证书","王五持有 CCSK 与 CISA 证书"],`+
			`"model":"qwen3-rerank","query":"谁有 CCSK 认证","top_n":3}`,
		string(sent))
}

func TestDecodesTheRecordedResponse(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(recordedResponse))
	}))
	defer server.Close()

	c := newClient(t, server.URL+defaultPath, api.RerankSettings{})
	got, err := c.Rerank(context.Background(), "谁有 CCSK 认证",
		[]string{"张三持有 CCSK 证书", "李四持有 CISP 证书", "王五持有 CCSK 与 CISA 证书"})
	require.NoError(t, err)

	// The scores are the vendor's, in the order it ranked them, and no text
	// travels with them: the shared layer reads it from the caller's slice.
	assert.Equal(t, []api.RerankResult{
		{Index: 2, Score: 0.8464465276226582},
		{Index: 0, Score: 0.798763089207101},
		{Index: 1, Score: 0.32370468862378365},
	}, got)
}

// DashScope answers some failures with a 200 and an error code in the body,
// which would otherwise decode as an empty result set.
func TestSurfacesAnInBodyError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"InvalidParameter","message":"documents is too long","request_id":"r"}`))
	}))
	defer server.Close()

	c := newClient(t, server.URL+defaultPath, api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "documents is too long")
}

// An index outside the documents that were sent cannot be scored by the
// caller, so it is an error rather than a result.
func TestRejectsAnOutOfRangeIndex(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","results":[{"index":3,"relevance_score":0.5}]}`))
	}))
	defer server.Close()

	c := newClient(t, server.URL+defaultPath, api.RerankSettings{})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

// The vendor's rerank base URL is the native text-rerank endpoint, which the
// catalog hands over as the row's base URL for every rerank model. A client
// built from a bare host must place the request on the compatibility route,
// and one built from the full route must not double it.
func TestResolvesTheCompatibilityPathWithoutDoublingIt(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	for _, tc := range []struct {
		name    string
		baseURL string
	}{
		{name: "bare host", baseURL: ""},
		{name: "already the full route", baseURL: defaultPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`{"object":"list","results":[]}`))
			}))
			defer server.Close()

			c := newClient(t, server.URL+tc.baseURL, api.RerankSettings{})
			_, err := c.Rerank(context.Background(), "q", []string{"d"})
			require.NoError(t, err)
			assert.Equal(t, defaultPath, gotPath)
		})
	}
}

// A declared path wins, which is what keeps this package usable behind a
// gateway that mounts the route elsewhere.
func TestHonoursADeclaredPath(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"object":"list","results":[]}`))
	}))
	defer server.Close()

	c := newClient(t, server.URL, api.RerankSettings{Path: "/gateway/reranks"})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.NoError(t, err)
	assert.Equal(t, "/gateway/reranks", gotPath)
}
