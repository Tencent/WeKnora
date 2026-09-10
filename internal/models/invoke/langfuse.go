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

// startEmbeddingLangfuse ports the v1 langfuseEmbedder input shapes
// (embedding/langfuse_wrapper.go): a single-input call logs the text, a batch
// logs count + short previews (full texts would flood theLangfuse network
// cost for ingestion-sized batches). Generation name is "embedding.embed"
// for both (v1 distinguished embed/batch_embed — recorded delta, P2).
func startEmbeddingLangfuse(ctx context.Context, m *ModelConfig, opts *EmbeddingOptions) *langfuseGen {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return &langfuseGen{ctx: ctx}
	}
	var input interface{}
	if len(opts.Inputs) == 1 {
		input = opts.Inputs[0]
	} else {
		input = map[string]interface{}{
			"count":   len(opts.Inputs),
			"preview": previewTexts(opts.Inputs, 5),
		}
	}
	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:  "embedding.embed",
		Model: m.ModelName,
		Input: input,
		Metadata: map[string]interface{}{
			"model_id":   m.ModelID,
			"dimensions": opts.Dimensions,
			"batch_size": len(opts.Inputs),
		},
	})
	return &langfuseGen{ctx: genCtx, gen: gen}
}

// finishEmbedding closes the observation with the v1 output shape (dimensions
// summary) and the approximated usage (runes/4 — langfuse cost reports need
// non-zero input tokens; embedding vendors rarely return usage).
func (g *langfuseGen) finishEmbedding(resp *EmbeddingResponse, inputs []string, err error) {
	if g.gen == nil {
		return
	}
	var output interface{}
	if resp != nil && len(resp.Vectors) > 0 {
		output = map[string]interface{}{
			"count":      len(resp.Vectors),
			"dimensions": len(resp.Vectors[0]),
		}
	}
	g.gen.Finish(output, approxEmbeddingUsage(inputs), err)
}

// approxEmbeddingUsage estimates input tokens as rune_count/4+1 per text
// (v1 approxEmbeddingUsage — proportional-only approximation, purely for
// cost reporting).
func approxEmbeddingUsage(texts []string) *langfuse.TokenUsage {
	total := 0
	for _, t := range texts {
		runes := len([]rune(t))
		if runes == 0 {
			continue
		}
		total += runes/4 + 1
	}
	if total == 0 {
		return nil
	}
	return &langfuse.TokenUsage{
		Input: total,
		Total: total,
		Unit:  "TOKENS",
	}
}

// previewTexts keeps at most n truncated input previews.
func previewTexts(texts []string, n int) []string {
	if len(texts) <= n {
		out := make([]string, len(texts))
		for i, t := range texts {
			out[i] = truncateRunes(t, 120)
		}
		return out
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = truncateRunes(texts[i], 120)
	}
	return out
}

func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}
