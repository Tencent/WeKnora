package invoke

// langfuse.go is the entry's built-in langfuse middleware mount point (design
// §6.4/§6.6): every entry call automatically produces a generation observation
// when the langfuse manager is enabled; deployments without langfuse pay an
// Enabled() check only.

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// langfuseGen tracks one in-flight generation observation.
type langfuseGen struct {
	ctx context.Context
	gen *langfuse.Generation
}

// startLangfuse begins a generation observation for an entry call. opts is
// serialized as the generation input; a disabled manager short-circuits.
func startLangfuse(ctx context.Context, name string, m *ModelConfig, opts any) *langfuseGen {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return &langfuseGen{ctx: ctx}
	}
	input, _ := json.Marshal(opts)
	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:  name,
		Model: m.ModelName,
		Input: json.RawMessage(input),
		Metadata: map[string]interface{}{
			"model_id":  m.ModelID,
			"provider":  m.Provider,
			"streaming": name == "chat.completion.stream",
		},
	})
	return &langfuseGen{ctx: genCtx, gen: gen}
}

// finish closes the observation with the response usage (chat only) or error.
func (g *langfuseGen) finish(resp *ChatResponse, err error) {
	if g.gen == nil {
		return
	}
	var usage *langfuse.TokenUsage
	var output interface{}
	if resp != nil {
		usage = &langfuse.TokenUsage{
			Input:  resp.Usage.PromptTokens,
			Output: resp.Usage.CompletionTokens,
			Total:  resp.Usage.TotalTokens,
		}
		output, _ = json.Marshal(resp)
	}
	g.gen.Finish(output, usage, err)
}
