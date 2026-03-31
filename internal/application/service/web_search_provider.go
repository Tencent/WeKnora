package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// webSearchProviderService implements interfaces.WebSearchProviderService
type webSearchProviderService struct {
	repo interfaces.WebSearchProviderRepository
}

// NewWebSearchProviderService creates a new web search provider service
func NewWebSearchProviderService(repo interfaces.WebSearchProviderRepository) interfaces.WebSearchProviderService {
	return &webSearchProviderService{repo: repo}
}

// CreateProvider creates a new web search provider configuration.
func (s *webSearchProviderService) CreateProvider(ctx context.Context, provider *types.WebSearchProviderEntity) error {
	if provider.TenantID == 0 {
		return fmt.Errorf("tenant ID is required")
	}

	if !isValidProviderType(provider.Provider) {
		return fmt.Errorf("invalid provider type: %s", provider.Provider)
	}

	if err := validateProviderParameters(provider.Provider, provider.Parameters); err != nil {
		return err
	}

	if provider.IsDefault {
		if err := s.repo.ClearDefault(ctx, provider.TenantID, ""); err != nil {
			logger.Warnf(ctx, "Failed to clear default providers: %v", err)
		}
	}

	logger.Infof(ctx, "Creating web search provider: tenant=%d, name=%s, type=%s", provider.TenantID, provider.Name, provider.Provider)
	return s.repo.Create(ctx, provider)
}

// UpdateProvider updates an existing provider.
func (s *webSearchProviderService) UpdateProvider(ctx context.Context, provider *types.WebSearchProviderEntity) error {
	if provider.TenantID == 0 {
		return fmt.Errorf("tenant ID is required")
	}

	// Validate provider type if set
	if provider.Provider != "" && !isValidProviderType(provider.Provider) {
		return fmt.Errorf("invalid provider type: %s", provider.Provider)
	}

	if provider.IsDefault {
		if err := s.repo.ClearDefault(ctx, provider.TenantID, provider.ID); err != nil {
			logger.Warnf(ctx, "Failed to clear default providers: %v", err)
		}
	}

	logger.Infof(ctx, "Updating web search provider: tenant=%d, id=%s", provider.TenantID, provider.ID)
	return s.repo.Update(ctx, provider)
}

// DeleteProvider deletes a provider by tenant + id.
func (s *webSearchProviderService) DeleteProvider(ctx context.Context, tenantID uint64, id string) error {
	logger.Infof(ctx, "Deleting web search provider: tenant=%d, id=%s", tenantID, id)
	return s.repo.Delete(ctx, tenantID, id)
}

// isValidProviderType checks if the given provider type is supported
func isValidProviderType(provider types.WebSearchProviderType) bool {
	switch provider {
	case types.WebSearchProviderTypeBing,
		types.WebSearchProviderTypeGoogle,
		types.WebSearchProviderTypeDuckDuckGo,
		types.WebSearchProviderTypeTavily:
		return true
	default:
		return false
	}
}

// validateProviderParameters validates required parameters for each provider type
func validateProviderParameters(provider types.WebSearchProviderType, params types.WebSearchProviderParameters) error {
	switch provider {
	case types.WebSearchProviderTypeBing:
		if params.APIKey == "" {
			return fmt.Errorf("API key is required for Bing provider")
		}
	case types.WebSearchProviderTypeGoogle:
		if params.APIKey == "" {
			return fmt.Errorf("API key is required for Google provider")
		}
		if params.EngineID == "" {
			return fmt.Errorf("engine ID is required for Google provider")
		}
	case types.WebSearchProviderTypeTavily:
		if params.APIKey == "" {
			return fmt.Errorf("API key is required for Tavily provider")
		}
	case types.WebSearchProviderTypeDuckDuckGo:
		// No API key required
	}
	return nil
}
