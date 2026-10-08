// Package bedrockrank implements AWS Bedrock Agent Runtime's SigV4-signed
// Rerank action through the official AWS SDK v2.
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_agent-runtime_Rerank.html
package bedrockrank

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
)

// DefaultRegion is the default AWS region for Bedrock reranking.
const DefaultRegion = "us-west-2"

var regionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)

// Config holds the Bedrock credentials, region, model, and optional SDK overrides.
type Config struct {
	AccessKey, SecretKey, Region, Model string
	// BaseURL is only for an explicit custom endpoint. Empty lets the SDK
	// choose the regional bedrock-agent-runtime endpoint from Region.
	BaseURL    string
	HTTPClient *http.Client
	// Client permits protocol tests to inject an SDK-shaped stand-in.
	Client bedrockagentruntime.RerankAPIClient
}

// Client calls the Bedrock Agent Runtime Rerank action.
type Client struct {
	modelARN string
	client   bedrockagentruntime.RerankAPIClient
}

// New creates a Bedrock rerank client with the supplied AWS credentials.
func New(cfg Config) (*Client, error) {
	accessKey, secretKey := strings.TrimSpace(cfg.AccessKey), strings.TrimSpace(cfg.SecretKey)
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("bedrock rerank requires AWS Access Key ID and Secret Access Key")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = DefaultRegion
	}
	if !regionPattern.MatchString(region) {
		return nil, fmt.Errorf("invalid AWS region %q for Bedrock rerank", region)
	}
	modelARN, err := modelARN(strings.TrimSpace(cfg.Model), region)
	if err != nil {
		return nil, err
	}
	client := cfg.Client
	if client == nil {
		awsCfg := aws.Config{
			Region: region,
			Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
				accessKey, secretKey, "")),
			HTTPClient: cfg.HTTPClient,
		}
		client = bedrockagentruntime.NewFromConfig(awsCfg, func(options *bedrockagentruntime.Options) {
			if cfg.BaseURL != "" {
				options.BaseEndpoint = aws.String(cfg.BaseURL)
			}
		})
	}
	return &Client{modelARN: modelARN, client: client}, nil
}

func modelARN(model, region string) (string, error) {
	if model == "" {
		return "", fmt.Errorf("bedrock rerank model is required")
	}
	if !strings.HasPrefix(model, "arn:") {
		return "arn:aws:bedrock:" + region + "::foundation-model/" + model, nil
	}
	parts := strings.SplitN(model, ":", 6)
	if len(parts) != 6 || parts[1] != "aws" || parts[2] != "bedrock" || parts[3] != region || parts[4] != "" ||
		!strings.HasPrefix(parts[5], "foundation-model/") || strings.TrimPrefix(parts[5], "foundation-model/") == "" {
		return "", fmt.Errorf("bedrock rerank model ARN must name a foundation model in region %s", region)
	}
	return model, nil
}

// Rerank scores the documents using Bedrock and returns results in ranked order.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	if len(documents) == 0 {
		return nil, nil
	}
	if len(documents) > 1000 {
		return nil, fmt.Errorf("bedrock rerank accepts at most 1000 sources per request")
	}
	if query == "" {
		return nil, fmt.Errorf("bedrock rerank query must not be empty")
	}
	sources := make([]types.RerankSource, len(documents))
	for i, text := range documents {
		if text == "" {
			return nil, fmt.Errorf("bedrock rerank source %d must not be empty", i)
		}
		sources[i] = types.RerankSource{
			Type: types.RerankSourceTypeInline,
			InlineDocumentSource: &types.RerankDocument{
				Type:         types.RerankDocumentTypeText,
				TextDocument: &types.RerankTextDocument{Text: aws.String(text)},
			},
		}
	}
	input := &bedrockagentruntime.RerankInput{
		Queries: []types.RerankQuery{{
			Type:      types.RerankQueryContentTypeText,
			TextQuery: &types.RerankTextDocument{Text: aws.String(query)},
		}},
		Sources: sources,
		RerankingConfiguration: &types.RerankingConfiguration{
			Type: types.RerankingConfigurationTypeBedrockRerankingModel,
			BedrockRerankingConfiguration: &types.BedrockRerankingConfiguration{
				ModelConfiguration: &types.BedrockRerankingModelConfiguration{ModelArn: aws.String(c.modelARN)},
				NumberOfResults:    aws.Int32(int32(len(documents))),
			},
		},
	}

	results := make([]api.RerankResult, 0, len(documents))
	seenIndexes := make([]bool, len(documents))
	seenTokens := make(map[string]bool)
	for {
		page, err := c.client.Rerank(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("bedrock rerank: %w", err)
		}
		if page == nil {
			return nil, fmt.Errorf("bedrock rerank returned an empty response")
		}
		for _, result := range page.Results {
			if result.Index == nil || result.RelevanceScore == nil {
				return nil, fmt.Errorf("bedrock rerank result is missing index or relevanceScore")
			}
			index := int(*result.Index)
			if index < 0 || index >= len(documents) {
				return nil, fmt.Errorf("bedrock rerank index %d out of range for %d documents", index, len(documents))
			}
			if seenIndexes[index] {
				return nil, fmt.Errorf("bedrock rerank returned duplicate index %d", index)
			}
			seenIndexes[index] = true
			results = append(results, api.RerankResult{
				Index: index, Score: float64(*result.RelevanceScore), Text: documents[index],
			})
		}
		if page.NextToken == nil || *page.NextToken == "" {
			break
		}
		if seenTokens[*page.NextToken] {
			return nil, fmt.Errorf("bedrock rerank returned a repeated pagination token")
		}
		seenTokens[*page.NextToken] = true
		input.NextToken = page.NextToken
	}
	if len(results) != len(documents) {
		return nil, fmt.Errorf("bedrock rerank returned %d scores for %d documents", len(results), len(documents))
	}
	return results, nil
}
