package adapters

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// Metadata-table integrity (migrated from v1 provider package tests): every
// provider the frontend is served must carry usable declarations.
func TestProvidersDataTableComplete(t *testing.T) {
	var names []invoke.ProviderName
	for _, name := range invoke.AllProviders() {
		info, ok := providerInfoFor(name)
		assert.True(t, ok, "provider %s missing from the metadata table", name)
		assert.Equal(t, name, info.Name)
		assert.NotEmpty(t, info.DisplayName)
		assert.NotEmpty(t, info.ModelTypes)
		names = append(names, name)
	}
	assert.GreaterOrEqual(t, len(names), 25)
}

func TestListProvidersByModelType(t *testing.T) {
	t.Run("chat models", func(t *testing.T) {
		chat := types.ModelTypeKnowledgeQA
		providers := ListProviders(&chat)
		assert.NotEmpty(t, providers)
		// Multiple providers support chat
		assert.GreaterOrEqual(t, len(providers), 9)
	})

	t.Run("rerank models", func(t *testing.T) {
		rerank := types.ModelTypeRerank
		providers := ListProviders(&rerank)
		assert.NotEmpty(t, providers)
		foundAliyun, foundLKEAP, foundVolcengine := false, false, false
		for _, p := range providers {
			switch p.Name {
			case invoke.ProviderAliyun:
				foundAliyun = true
			case invoke.ProviderLKEAP:
				foundLKEAP = true
				assert.Equal(t, invoke.LKEAPRerankBaseURL, p.GetDefaultURL(types.ModelTypeRerank))
			case invoke.ProviderVolcengine:
				foundVolcengine = true
				assert.Equal(t, invoke.VolcengineRerankBaseURL, p.GetDefaultURL(types.ModelTypeRerank))
			}
		}
		assert.True(t, foundAliyun, "Aliyun should support rerank")
		assert.True(t, foundLKEAP, "LKEAP should support rerank")
		assert.True(t, foundVolcengine, "Volcengine should support rerank")
	})

	t.Run("unfiltered returns canonical order", func(t *testing.T) {
		providers := ListProviders(nil)
		assert.Len(t, providers, len(invoke.AllProviders()))
		for i, name := range invoke.AllProviders() {
			assert.Equal(t, name, providers[i].Name)
		}
	})
}

// Capability-declaration sanity across the table (migrated from the v1
// provider package): thinking declarations stay self-consistent.
func TestRegisteredProvidersHaveUsableCapabilities(t *testing.T) {
	for _, info := range ListProviders(nil) {
		caps := info.EffectiveCapabilities()
		if caps.Chat == nil || !caps.Chat.Thinking.Supported {
			continue
		}
		th := caps.Chat.Thinking
		if th.DefaultLevel != "" {
			assert.Contains(t, th.SupportedLevels, th.DefaultLevel,
				"provider %s: DefaultLevel must be in SupportedLevels", info.Name)
		}
		assert.NotEmpty(t, th.SupportedLevels,
			"provider %s: supported thinking must declare levels", info.Name)
	}
}
