package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	infra_web_search "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// WebSearchService provides web search functionality.
// It resolves provider configurations from the database and creates provider
// instances on-demand via the infrastructure registry.
type WebSearchService struct {
	registry     *infra_web_search.Registry
	providerRepo interfaces.WebSearchProviderRepository
	timeout      int
}

// NewWebSearchService creates a new web search service.
// The registry holds provider type factories; the providerRepo loads tenant-specific configurations.
func NewWebSearchService(
	cfg *config.Config,
	registry *infra_web_search.Registry,
	providerRepo interfaces.WebSearchProviderRepository,
) (interfaces.WebSearchService, error) {
	timeout := 10 // default timeout in seconds
	if cfg.WebSearch != nil && cfg.WebSearch.Timeout > 0 {
		timeout = cfg.WebSearch.Timeout
	}

	return &WebSearchService{
		registry:     registry,
		providerRepo: providerRepo,
		timeout:      timeout,
	}, nil
}

// Resolve loads providerID once and binds it to the clients registered for its type and to the
// tenant's blacklist.
func (s *WebSearchService) Resolve(ctx context.Context, providerID string) (interfaces.WebClient, error) {
	var cfg *types.WebSearchConfig
	if tenant, ok := ctx.Value(types.TenantInfoContextKey).(*types.Tenant); ok && tenant != nil {
		cfg = tenant.WebSearchConfig
	}
	providerType, params, err := resolveProviderCall(ctx, s.providerRepo, strings.TrimSpace(providerID), cfg)
	if err != nil {
		return nil, err
	}
	capabilities, err := s.registry.Capabilities(providerType)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(s.timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := &webClient{
		registry:     s.registry,
		providerType: providerType,
		params:       params,
		capabilities: capabilities,
		timeout:      timeout,
	}
	if cfg != nil {
		client.blacklist = cfg.Blacklist
	}
	// The fetch client is built once so a run reuses its connections.
	client.fetcher, err = s.registry.NewFetcher(providerType, params)
	if err != nil {
		logger.Warnf(ctx, "[WebFetch] %s fetch client unavailable, using local reader: %v", providerType, err)
		client.fetcher = infra_web_search.LocalFetcher()
	}
	return client, nil
}

// webClient is one stored provider resolved by WebSearchService.Resolve.
type webClient struct {
	registry     *infra_web_search.Registry
	providerType string
	params       types.WebSearchProviderParameters
	capabilities types.WebSearchCapabilities
	timeout      time.Duration
	blacklist    []string
	fetcher      interfaces.WebFetcher
}

func (c *webClient) Capabilities() types.WebSearchCapabilities {
	return c.capabilities
}

// Search builds the search client per call so a bad credential surfaces as this call's error.
func (c *webClient) Search(ctx context.Context, req types.WebSearchRequest) ([]*types.WebSearchResult, error) {
	if req.Filters != (types.WebSearchFilters{}) {
		if !c.capabilities.Filters {
			return nil, fmt.Errorf("provider %s does not support country/freshness filters; "+
				"omit them or select Brave or Serply", c.providerType)
		}
		if err := req.Filters.Validate(); err != nil {
			return nil, err
		}
	}
	searcher, err := c.registry.NewSearcher(c.providerType, c.params)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider %s: %w", c.providerType, err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	results, err := searcher.Search(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("web search failed: %w", err)
	}
	return filterBlacklist(results, c.blacklist), nil
}

func (c *webClient) Fetch(ctx context.Context, pageURL string) (*types.WebPage, error) {
	return c.fetcher.Fetch(ctx, pageURL)
}

// resolveProviderCall loads the saved WebSearchProviderEntity for this call.
// The provider ID is chosen by the caller: an agent override, or the tenant's
// default provider. WebSearchConfig no longer selects the engine or its API key.
func resolveProviderCall(
	ctx context.Context,
	providerRepo interfaces.WebSearchProviderRepository,
	providerID string,
	cfg *types.WebSearchConfig,
) (string, types.WebSearchProviderParameters, error) {
	if providerID == "" {
		return "", types.WebSearchProviderParameters{}, fmt.Errorf("no web search provider configured")
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return "", types.WebSearchProviderParameters{}, fmt.Errorf("workspace ID not found in context")
	}
	entity, err := providerRepo.GetByID(ctx, tenantID, providerID)
	if err != nil {
		return "", types.WebSearchProviderParameters{}, fmt.Errorf(
			"failed to load web search provider %s: %w", providerID, err)
	}
	if entity == nil {
		return "", types.WebSearchProviderParameters{}, fmt.Errorf("web search provider not found: %s", providerID)
	}
	return string(entity.Provider), mergeProxyFromWebSearchConfig(entity.Parameters, cfg), nil
}

// mergeProxyFromWebSearchConfig applies cfg.ProxyURL over stored provider params when non-empty (call-time override).
func mergeProxyFromWebSearchConfig(base types.WebSearchProviderParameters, cfg *types.WebSearchConfig) types.WebSearchProviderParameters {
	if cfg != nil {
		if pu := strings.TrimSpace(cfg.ProxyURL); pu != "" {
			base.ProxyURL = pu
		}
	}
	return base
}

// filterBlacklist filters results based on blacklist rules
func filterBlacklist(
	results []*types.WebSearchResult,
	blacklist []string,
) []*types.WebSearchResult {
	if len(blacklist) == 0 {
		return results
	}

	filtered := make([]*types.WebSearchResult, 0, len(results))

	for _, result := range results {
		shouldFilter := false

		for _, rule := range blacklist {
			if matchesBlacklistRule(result.URL, rule) {
				shouldFilter = true
				break
			}
		}

		if !shouldFilter {
			filtered = append(filtered, result)
		}
	}

	return filtered
}

// matchesBlacklistRule checks if a URL matches a blacklist rule
// Supports both pattern matching (e.g., *://*.example.com/*) and regex patterns (e.g., /example\.(net|org)/)
func matchesBlacklistRule(url, rule string) bool {
	// Check if it's a regex pattern (starts and ends with /)
	if strings.HasPrefix(rule, "/") && strings.HasSuffix(rule, "/") {
		pattern := rule[1 : len(rule)-1]
		matched, err := regexp.MatchString(pattern, url)
		if err != nil {
			logger.Warnf(context.Background(), "Invalid regex pattern in blacklist: %s, error: %v", rule, err)
			return false
		}
		return matched
	}

	// Pattern matching (e.g., *://*.example.com/*)
	pattern := strings.ReplaceAll(rule, "*", ".*")
	pattern = "^" + pattern + "$"
	matched, err := regexp.MatchString(pattern, url)
	if err != nil {
		logger.Warnf(context.Background(), "Invalid pattern in blacklist: %s, error: %v", rule, err)
		return false
	}
	return matched
}

// ConvertWebSearchResults converts WebSearchResult to SearchResult
func ConvertWebSearchResults(webResults []*types.WebSearchResult) []*types.SearchResult {
	return searchutil.ConvertWebSearchResults(
		webResults,
		searchutil.WithSeqFunc(func(idx int) int { return idx }),
	)
}
