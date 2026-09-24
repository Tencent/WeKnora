package parity

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// This fixture was captured before the provider/runtime refactor. It pins every
// catalog entry, alias and family rule, including full resolved protocol options,
// endpoints and request bodies at every reasoning level. Updating it is an
// explicit compatibility decision, never part of normal model generation.
func TestCatalogMigrationBaseline(t *testing.T) {
	actual := map[string]string{}
	for _, vendor := range modelruntime.List() {
		for _, entry := range vendor.Models() {
			names := append([]string{entry.ID}, entry.Aliases...)
			if entry.Match != "" {
				names = append(names, strings.ReplaceAll(entry.Match, "*", "snapshot"))
			}
			for _, name := range names {
				if name == "" {
					continue
				}
				key := fmt.Sprintf("%s/%s/%s", vendor.ID, entry.Type, name)
				r, err := modelruntime.Resolve(
					modelruntime.Ref{
						Provider:  vendor.ID,
						Model:     name,
						ModelType: entry.Type,
					},
				)
				if err != nil {
					actual[key] = "error: " + err.Error()
					continue
				}
				state := map[string]any{"resolved": r, "auth": vendor.AuthStyleFor(r.API)}
				if vendor.Endpoint != nil {
					u, q := vendor.Endpoint(
						providers.EndpointRequest{
							BaseURL:      r.BaseURL,
							Model:        name,
							ModelType:    entry.Type,
							API:          r.API,
							EmbeddingAPI: r.EmbeddingAPI,
						},
					)
					state["endpoint"], state["query"] = u, q
				}
				r.Vendor = nil // functions are not serializable; their output is captured above
				if entry.Type == "" || entry.Type == types.ModelTypeKnowledgeQA {
					bodies := map[string]any{}
					for _, level := range api.ReasoningLadder {
						for _, stream := range []bool{false, true} {
							bodies[fmt.Sprintf("%s/%t", level, stream)] = buildBody(t,
								&chat.ChatConfig{
									Provider:  vendor.ID,
									ModelName: name,
									AppID:     "app",
									AppSecret: "secret",
									BaseURL: reachableBaseURL(
										vendor.GetDefaultURL(
											types.ModelTypeKnowledgeQA,
										),
									),
								},
								&api.Options{
									MaxTokens:       256,
									Temperature:     0.5,
									ReasoningEffort: level,
									Thinking: ptrBool(
										level != api.ReasoningOff,
									),
								}, stream)
						}
					}
					state["bodies"] = bodies
				}
				data, err := json.Marshal(state)
				require.NoError(t, err, key)
				actual[key] = fmt.Sprintf("%x", sha256.Sum256(data))
			}
		}
	}
	const path = "testdata/catalog-baseline.json"
	if os.Getenv("WEKNORA_UPDATE_MODEL_BASELINE") == "1" {
		data, err := json.MarshalIndent(actual, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o644))
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var expected map[string]string
	require.NoError(t, json.Unmarshal(data, &expected))
	require.Equal(
		t,
		expected,
		actual,
		"catalog or wire contract changed; inspect each difference before updating the baseline",
	)
	t.Logf("verified %d catalog entries, aliases and family matches", len(actual))
}
