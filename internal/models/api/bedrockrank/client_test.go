package bedrockrank

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stub struct {
	calls []*bedrockagentruntime.RerankInput
	pages []*bedrockagentruntime.RerankOutput
	err   error
}

func (s *stub) Rerank(
	_ context.Context, input *bedrockagentruntime.RerankInput, _ ...func(*bedrockagentruntime.Options),
) (*bedrockagentruntime.RerankOutput, error) {
	inputCopy := *input
	s.calls = append(s.calls, &inputCopy)
	if s.err != nil {
		return nil, s.err
	}
	if len(s.pages) == 0 {
		return nil, nil
	}
	page := s.pages[0]
	s.pages = s.pages[1:]
	return page, nil
}

func scored(index int32, score float32) types.RerankResult {
	return types.RerankResult{Index: aws.Int32(index), RelevanceScore: aws.Float32(score)}
}

func testClient(t *testing.T, s *stub) *Client {
	t.Helper()
	c, err := New(Config{
		AccessKey: "AKIATEST", SecretKey: "secret", Region: "us-west-2",
		Model: "amazon.rerank-v1:0", Client: s,
	})
	require.NoError(t, err)
	return c
}

func TestBuildsBedrockRequestAndMapsOriginalIndexes(t *testing.T) {
	s := &stub{pages: []*bedrockagentruntime.RerankOutput{{
		Results: []types.RerankResult{scored(2, 0.9), scored(0, 0.5), scored(1, 0.1)},
	}}}
	c := testClient(t, s)
	got, err := c.Rerank(context.Background(), "query", []string{"same", "other", "same"})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []int{2, 0, 1}, []int{got[0].Index, got[1].Index, got[2].Index})
	assert.Equal(t, []string{"same", "same", "other"}, []string{got[0].Text, got[1].Text, got[2].Text})
	assert.InDelta(t, 0.9, got[0].Score, 1e-6)

	require.Len(t, s.calls, 1)
	in := s.calls[0]
	require.Len(t, in.Queries, 1)
	assert.Equal(t, types.RerankQueryContentTypeText, in.Queries[0].Type)
	assert.Equal(t, "query", aws.ToString(in.Queries[0].TextQuery.Text))
	require.Len(t, in.Sources, 3)
	for i, source := range in.Sources {
		assert.Equal(t, types.RerankSourceTypeInline, source.Type)
		assert.Equal(t, types.RerankDocumentTypeText, source.InlineDocumentSource.Type)
		assert.Equal(t,
			[]string{"same", "other", "same"}[i], aws.ToString(source.InlineDocumentSource.TextDocument.Text),
		)
	}
	config := in.RerankingConfiguration
	assert.Equal(t, types.RerankingConfigurationTypeBedrockRerankingModel, config.Type)
	assert.Equal(t, "arn:aws:bedrock:us-west-2::foundation-model/amazon.rerank-v1:0",
		aws.ToString(config.BedrockRerankingConfiguration.ModelConfiguration.ModelArn))
	assert.Equal(t, int32(3), aws.ToInt32(config.BedrockRerankingConfiguration.NumberOfResults))
}

func TestCollectsAllPages(t *testing.T) {
	s := &stub{pages: []*bedrockagentruntime.RerankOutput{
		{Results: []types.RerankResult{scored(1, 0.8)}, NextToken: aws.String("page-2")},
		{Results: []types.RerankResult{scored(0, 0.2)}},
	}}
	c := testClient(t, s)
	got, err := c.Rerank(context.Background(), "q", []string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 0}, []int{got[0].Index, got[1].Index})
	require.Len(t, s.calls, 2)
	assert.Nil(t, s.calls[0].NextToken)
	assert.Equal(t, "page-2", aws.ToString(s.calls[1].NextToken))
}

func TestRejectsBadConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     Config
		message string
	}{
		{"missing access key", Config{SecretKey: "s", Model: "m"}, "Access Key ID"},
		{"missing secret", Config{AccessKey: "k", Model: "m"}, "Secret Access Key"},
		{"invalid region", Config{
			AccessKey: "k", SecretKey: "s", Region: "evil.example", Model: "m",
		}, "invalid AWS region"},
		{"missing model", Config{AccessKey: "k", SecretKey: "s"}, "model is required"},
		{"ARN region mismatch", Config{
			AccessKey: "k", SecretKey: "s", Region: "us-west-2",
			Model: "arn:aws:bedrock:us-east-1::foundation-model/amazon.rerank-v1:0",
		}, "region us-west-2"},
		{"ARN has account", Config{
			AccessKey: "k", SecretKey: "s", Region: "us-west-2",
			Model: "arn:aws:bedrock:us-west-2:123456789012:foundation-model/amazon.rerank-v1:0",
		}, "foundation model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.message)
		})
	}
}

func TestRejectsMalformedAndIncompleteResponses(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		pages         []*bedrockagentruntime.RerankOutput
		docs          []string
	}{
		{"nil response", "empty response", []*bedrockagentruntime.RerankOutput{nil}, []string{"a"}},
		{"missing score", "missing index or relevanceScore", []*bedrockagentruntime.RerankOutput{{
			Results: []types.RerankResult{{Index: aws.Int32(0)}},
		}}, []string{"a"}},
		{"out of range", "out of range", []*bedrockagentruntime.RerankOutput{{
			Results: []types.RerankResult{scored(9, 0.5)},
		}}, []string{"a"}},
		{"duplicate index", "duplicate index", []*bedrockagentruntime.RerankOutput{{
			Results: []types.RerankResult{scored(0, 0.5), scored(0, 0.4)},
		}}, []string{"a", "b"}},
		{"incomplete", "returned 1 scores", []*bedrockagentruntime.RerankOutput{{
			Results: []types.RerankResult{scored(0, 0.5)},
		}}, []string{"a", "b"}},
		{"repeated token", "repeated pagination token", []*bedrockagentruntime.RerankOutput{
			{Results: []types.RerankResult{scored(0, 0.5)}, NextToken: aws.String("same")},
			{NextToken: aws.String("same")},
		}, []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, &stub{pages: tc.pages})
			_, err := c.Rerank(context.Background(), "q", tc.docs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.message)
		})
	}
}

func TestSurfacesSDKError(t *testing.T) {
	c := testClient(t, &stub{err: errors.New("access denied")})
	_, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "access denied")
}

func TestSDKSignsTheAgentRuntimeWireFormat(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var path, authorization string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, authorization = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"index":1,"relevanceScore":0.8},{"index":0,"relevanceScore":0.2}]}`))
	}))
	defer server.Close()
	c, err := New(Config{
		AccessKey: "AKIATEST", SecretKey: "secret", Region: "us-west-2", Model: "cohere.rerank-v3-5:0",
		BaseURL: server.URL, HTTPClient: api.HTTPClient,
	})
	require.NoError(t, err)
	got, err := c.Rerank(context.Background(), "q", []string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "/rerank", path)
	assert.True(t, strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential=AKIATEST/"), authorization)
	assert.NotContains(t, authorization, "secret")
	assert.Equal(t, "q", body["queries"].([]any)[0].(map[string]any)["textQuery"].(map[string]any)["text"])
	assert.Equal(t, "INLINE", body["sources"].([]any)[0].(map[string]any)["type"])
	config := body["rerankingConfiguration"].(map[string]any)
	bedrockConfig := config["bedrockRerankingConfiguration"].(map[string]any)
	assert.Equal(t, float64(2), bedrockConfig["numberOfResults"])
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRegionChangesSDKEndpointAndSigningScope(t *testing.T) {
	var host, authorization string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		host, authorization = req.URL.Host, req.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"results":[{"index":0,"relevanceScore":0.7}]}`)),
			Request:    req,
		}, nil
	})}
	c, err := New(Config{
		AccessKey: "AKIATEST", SecretKey: "secret", Region: "us-east-1",
		Model: "cohere.rerank-v3-5:0", HTTPClient: client,
	})
	require.NoError(t, err)
	_, err = c.Rerank(context.Background(), "q", []string{"d"})
	require.NoError(t, err)
	assert.Equal(t, "bedrock-agent-runtime.us-east-1.amazonaws.com", host)
	assert.Contains(t, authorization, "/us-east-1/bedrock/")
}
