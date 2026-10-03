package runtime_test

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRuntimeOwnsDefinitionsAndQueryResults(t *testing.T) {
	rt := modelruntime.New()
	definition := &providers.Definition{
		ID: "owned", Name: "Owned", Auth: providers.AuthBearer,
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA},
		Headers:    map[string]string{"X-Owner": "original"},
		Compat: providers.VendorCompat{OpenAICompletions: api.OpenAICompletionsCompat{
			ExtraBody: map[string]any{"nested": map[string]any{"value": "original", "empty": nil}},
		}},
	}
	entry := models.ModelSpec{
		ID: "sample", Input: []string{"text", "image"},
		Cost:           &models.ModelCost{Input: 1},
		ThinkingLevels: api.ThinkingLevelMap{api.ReasoningHigh: api.StringPtr("high")},
		Compat:         json.RawMessage(`{"supports_temperature":false}`),
	}
	rt.Register(definition, entry)
	definition.Headers["X-Owner"] = "input mutation"
	definition.Compat.OpenAICompletions.ExtraBody["nested"].(map[string]any)["value"] = "input mutation"
	entry.Input[0], entry.Cost.Input = "audio", 99

	p, ok := rt.Get("owned")
	require.True(t, ok)
	check := func() {
		t.Helper()
		resolved, err := rt.Resolve(modelruntime.Ref{Provider: "owned", Model: "sample"})
		require.NoError(t, err)
		require.Equal(t, "original", resolved.Vendor.Headers["X-Owner"])
		require.Equal(t, "original", resolved.OpenAICompletions.ExtraBody["nested"].(map[string]any)["value"])
		require.Nil(t, resolved.OpenAICompletions.ExtraBody["nested"].(map[string]any)["empty"])
		require.Equal(t, []string{"text", "image"}, resolved.Spec.Input)
		require.Equal(t, float64(1), resolved.Spec.Cost.Input)
		require.Equal(t, "high", *resolved.Spec.ThinkingLevels[api.ReasoningHigh])
		require.False(t, resolved.OpenAICompletions.SupportsTemperature)
		resolved.Vendor.Headers["X-Owner"] = "resolved mutation"
		resolved.OpenAICompletions.ExtraBody["nested"].(map[string]any)["value"] = "resolved mutation"
	}
	check()
	p.Headers["X-Owner"] = "query mutation"
	p.Compat.OpenAICompletions.ExtraBody["nested"].(map[string]any)["value"] = "query mutation"
	for _, entries := range [][]models.ModelSpec{p.Models(), p.ModelsByType(types.ModelTypeKnowledgeQA)} {
		entries[0].Input[0], entries[0].Cost.Input = "audio", 99
		*entries[0].ThinkingLevels[api.ReasoningHigh] = "changed"
		entries[0].Compat[0] = 'x'
	}
	for _, view := range rt.List() {
		if view.ID == "owned" {
			view.Headers["X-Owner"] = "list mutation"
		}
	}
	check()
	// Rebuilding from the baseline must not reintroduce any input/query mutations.
	require.NoError(t, rt.Reload([]byte(`{"providers":{}}`), ""))
	check()
}

func TestRuntimeInstancesAndRetainedViewsAreIndependent(t *testing.T) {
	first, second := modelruntime.New(), modelruntime.New()
	old, ok := first.Get("openai")
	require.True(t, ok)
	ref := modelruntime.Ref{Provider: "openai", Model: "gpt-5"}
	before, err := old.Resolve(ref)
	require.NoError(t, err)
	require.NoError(t, first.Reload([]byte(`{"providers":{"openai":{
 "base_url":"https://relay.example/v1", "models":[{"id":"gpt-5","context_window":12345}]
}}}`), ""))
	changed, err := first.Resolve(ref)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example/v1", changed.BaseURL)
	require.Equal(t, 12345, changed.Spec.ContextWindow)
	for _, resolve := range []func(modelruntime.Ref) (*modelruntime.Resolved, error){old.Resolve, second.Resolve} {
		unchanged, err := resolve(ref)
		require.NoError(t, err)
		require.Equal(t, before.BaseURL, unchanged.BaseURL)
		require.Equal(t, before.Spec, unchanged.Spec)
	}
	require.NoError(t, first.Reload([]byte(`{"providers":{}}`), ""))
	restored, err := first.Resolve(ref)
	require.NoError(t, err)
	require.Equal(t, before.Spec, restored.Spec)
}

func TestResolveKeepsOneGenerationDuringReload(t *testing.T) {
	rt := modelruntime.New()
	overlay := func(generation int) []byte {
		return []byte(fmt.Sprintf(`{"providers":{"openai":{
 "base_url":"https://generation-%d.example/v1", "headers":{"X-Generation":"%d"},
 "models":[{"id":"gpt-5","context_window":%d}]
}}}`, generation, generation, generation))
	}
	require.NoError(t, rt.Reload(overlay(1), ""))
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				resolved, err := rt.Resolve(modelruntime.Ref{Provider: "openai", Model: "gpt-5"})
				if err != nil {
					t.Errorf("resolve: %v", err)
					return
				}
				generation := resolved.Vendor.Headers["X-Generation"]
				if resolved.BaseURL != "https://generation-"+generation+".example/v1" ||
					fmt.Sprint(resolved.Spec.ContextWindow) != generation {
					t.Errorf("mixed provider/catalog generation: %s, %s, %d",
						generation, resolved.BaseURL, resolved.Spec.ContextWindow)
				}
				resolved.Vendor.Headers["X-Generation"] = "caller mutation"
			}
		}()
	}
	for generation := 2; generation < 12; generation++ {
		require.NoError(t, rt.Reload(overlay(generation), ""))
	}
	wg.Wait()
}
