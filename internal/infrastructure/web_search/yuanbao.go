package web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tchttp "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/http"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

const yuanbaoEndpoint = "wsa.tencentcloudapi.com"

type YuanbaoProvider struct {
	client *common.Client
}

func ValidateYuanbaoParameters(params types.WebSearchProviderParameters) error {
	if strings.TrimSpace(params.SecretID) == "" {
		return fmt.Errorf("SecretId is required for Yuanbao provider")
	}
	if strings.TrimSpace(params.APIKey) == "" {
		return fmt.Errorf("SecretKey is required for Yuanbao provider")
	}
	return nil
}

func NewYuanbaoProvider(params types.WebSearchProviderParameters) (interfaces.WebSearcher, error) {
	if err := ValidateYuanbaoParameters(params); err != nil {
		return nil, err
	}
	if err := ValidateProxyURL(params.ProxyURL); err != nil {
		return nil, err
	}
	cp := profile.NewClientProfile()
	cp.HttpProfile.Endpoint = yuanbaoEndpoint
	cp.HttpProfile.ReqMethod = "POST"
	cp.HttpProfile.Proxy = params.ProxyURL
	cp.HttpProfile.ReqTimeout = 30
	credential := common.NewCredential(strings.TrimSpace(params.SecretID), strings.TrimSpace(params.APIKey))
	return &YuanbaoProvider{client: common.NewCommonClient(credential, "", cp)}, nil
}

func (p *YuanbaoProvider) Name() string { return "yuanbao" }

func (p *YuanbaoProvider) Search(ctx context.Context, request types.WebSearchRequest) ([]*types.WebSearchResult, error) {
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return nil, fmt.Errorf("query is empty")
	}
	maxResults := request.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 50 {
		maxResults = 50
	}
	apiRequest := tchttp.NewCommonRequest("wsa", "2025-05-08", "SearchPro")
	apiRequest.SetContext(ctx)
	if err := apiRequest.SetActionParameters(map[string]interface{}{"Query": query}); err != nil {
		return nil, fmt.Errorf("Yuanbao request: %w", err)
	}
	apiResponse := tchttp.NewCommonResponse()
	if err := p.client.Send(apiRequest, apiResponse); err != nil {
		return nil, fmt.Errorf("Yuanbao search: %w", err)
	}
	var response struct {
		Response struct {
			Pages []string `json:"Pages"`
			Msg   string   `json:"Msg"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(apiResponse.GetBody(), &response); err != nil {
		return nil, fmt.Errorf("Yuanbao response: %w", err)
	}
	results := make([]*types.WebSearchResult, 0, min(len(response.Response.Pages), maxResults))
	for _, raw := range response.Response.Pages {
		var page struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Passage string `json:"passage"`
			Content string `json:"content"`
			Date    string `json:"date"`
		}
		if err := json.Unmarshal([]byte(raw), &page); err != nil || strings.TrimSpace(page.URL) == "" {
			continue
		}
		result := &types.WebSearchResult{
			Title: page.Title, URL: page.URL, Snippet: page.Passage,
			Content: page.Content, Source: "yuanbao",
		}
		if request.IncludeDate {
			// Yuanbao dates have no timezone; preserve the provider value without
			// inventing an instant in the server's local timezone.
			result.Age = page.Date
		}
		results = append(results, result)
		if len(results) >= maxResults {
			break
		}
	}
	return results, nil
}
