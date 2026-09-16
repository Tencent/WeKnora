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
	metadata := map[string]interface{}{
		"model_id":  m.ModelID,
		"provider":  m.Provider,
		"streaming": name == "chat.completion.stream",
	}
	// v1 buildLangfuseChatMetadata's tool block (upstream 2026-09). v1's
	// call_purpose / prompt_prefix_fingerprint keys are wrapper-level caller
	// knowledge and stay v1-only (recorded delta): the entry cannot know the
	// call purpose.
	if chatOpts, ok := opts.(*ChatOptions); ok {
		for k, v := range buildLangfuseToolMetadata(chatOpts) {
			metadata[k] = v
		}
	}
	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:     name,
		Model:    m.ModelName,
		Input:    json.RawMessage(input),
		Metadata: metadata,
	})
	return &langfuseGen{ctx: genCtx, gen: gen}
}

const (
	langfuseDiscoverMCPTool = "discover_mcp_tools"
	langfuseMCPCatalogRunes = 8000
)

// buildLangfuseToolMetadata describes the call's tool surface: ordered tool
// names, plus the MCP catalog — the discover tool's description carries the
// serialized server catalog, truncated to keep the observation payload bounded.
func buildLangfuseToolMetadata(opts *ChatOptions) map[string]interface{} {
	if opts == nil || len(opts.Tools) == 0 {
		return nil
	}
	meta := map[string]interface{}{"has_tools": true}
	names := make([]string, 0, len(opts.Tools))
	for _, tool := range opts.Tools {
		names = append(names, tool.Name)
		if tool.Name == langfuseDiscoverMCPTool && tool.Description != "" {
			meta["mcp_catalog"] = truncateRunes(tool.Description, langfuseMCPCatalogRunes)
		}
	}
	meta["tool_names"] = names
	return meta
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

// --- rerank + ASR generation observations (P3, ports of the v1
// rerank/langfuse_wrapper.go and asr/langfuse_wrapper.go) ---

const (
	langfuseRerankPreviewDocs = 8
	langfuseRerankMaxScores   = 50
)

// startRerankLangfuse opens the "rerank" generation (v1 input/metadata shape:
// query + document previews, model_id/num_queries/total_chars/avg_doc_chars).
func startRerankLangfuse(ctx context.Context, m *ModelConfig, opts *RerankOptions) *langfuseGen {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return &langfuseGen{ctx: ctx}
	}
	totalChars := len([]rune(opts.Query))
	for _, doc := range opts.Documents {
		totalChars += len([]rune(doc))
	}
	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:  "rerank",
		Model: m.ModelName,
		Input: map[string]interface{}{
			"query":             opts.Query,
			"document_count":    len(opts.Documents),
			"documents_preview": previewDocs(opts.Documents, langfuseRerankPreviewDocs),
		},
		Metadata: map[string]interface{}{
			"model_id":      m.ModelID,
			"num_queries":   1,
			"total_chars":   totalChars,
			"avg_doc_chars": avgDocChars(opts.Documents),
		},
	})
	return &langfuseGen{ctx: genCtx, gen: gen}
}

// finishRerank closes the observation with the v1 output shape (summarized
// top results + score stats + document previews) and the approximated token
// usage (query + documents runes/4 — rerank vendors bill per 1K documents).
func (g *langfuseGen) finishRerank(resp *RerankResponse, opts *RerankOptions, err error) {
	if g.gen == nil {
		return
	}
	output := map[string]interface{}{
		"total_count": 0,
		"score_stats": nil,
	}
	var usage *langfuse.TokenUsage
	if resp != nil {
		output["results"] = summarizeResults(resp.Results, opts.Documents, langfuseRerankMaxScores)
		output["total_count"] = len(resp.Results)
		output["score_stats"] = scoreStats(resp.Results)
		if len(resp.Results) > langfuseRerankMaxScores {
			output["truncated"] = len(resp.Results) - langfuseRerankMaxScores
		}
	}
	usage = approxRerankUsage(opts.Query, opts.Documents)
	g.gen.Finish(output, usage, err)
}

// approxRerankUsage estimates input tokens as rune_count/4+1 per text
// (v1 approxRerankUsage).
func approxRerankUsage(query string, documents []string) *langfuse.TokenUsage {
	total := len([]rune(query))/4 + 1
	for _, d := range documents {
		total += len([]rune(d))/4 + 1
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

func avgDocChars(documents []string) int {
	if len(documents) == 0 {
		return 0
	}
	total := 0
	for _, doc := range documents {
		total += len([]rune(doc))
	}
	return total / len(documents)
}

func scoreStats(results []RerankResult) map[string]interface{} {
	if len(results) == 0 {
		return nil
	}
	minScore := results[0].Score
	maxScore := results[0].Score
	sum := 0.0
	for _, r := range results {
		if r.Score < minScore {
			minScore = r.Score
		}
		if r.Score > maxScore {
			maxScore = r.Score
		}
		sum += r.Score
	}
	return map[string]interface{}{
		"min": minScore,
		"max": maxScore,
		"avg": sum / float64(len(results)),
	}
}

func previewDocs(docs []string, n int) []map[string]interface{} {
	if len(docs) < n {
		n = len(docs)
	}
	out := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, map[string]interface{}{
			"index":   i,
			"preview": truncateRunes(docs[i], 160),
			"length":  len([]rune(docs[i])),
		})
	}
	return out
}

// summarizeResults previews the top n results; the document preview is
// re-derived from the input documents by index (v1 used the vendor echo —
// same content; P3 review finding 5 restores the v1 "preview" key).
func summarizeResults(results []RerankResult, documents []string, n int) []map[string]interface{} {
	if len(results) < n {
		n = len(results)
	}
	out := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		row := map[string]interface{}{
			"rank":        i + 1,
			"index":       results[i].Index,
			"model_score": results[i].Score,
		}
		idx := results[i].Index
		if idx >= 0 && idx < len(documents) {
			row["preview"] = truncateRunes(documents[idx], 160)
		}
		out = append(out, row)
	}
	return out
}

// startASRLangfuse opens the "asr.transcribe" generation (v1 shape: file
// name + audio size; audio bytes are never uploaded).
func startASRLangfuse(ctx context.Context, m *ModelConfig, opts *ASROptions) *langfuseGen {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return &langfuseGen{ctx: ctx}
	}
	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:  "asr.transcribe",
		Model: m.ModelName,
		Input: map[string]interface{}{
			"file_name":  opts.FileName,
			"audio_size": len(opts.Audio),
		},
		Metadata: map[string]interface{}{
			"model_id":   m.ModelID,
			"audio_size": len(opts.Audio),
		},
	})
	return &langfuseGen{ctx: genCtx, gen: gen}
}

// finishASR closes the observation: text + segment count + duration from the
// last segment end; ASR is billed per second, so the duration rides usage.
func (g *langfuseGen) finishASR(resp *ASRResponse, err error) {
	if g.gen == nil {
		return
	}
	output := map[string]interface{}{}
	var usage *langfuse.TokenUsage
	if resp != nil {
		output["text"] = resp.Text
		output["segment_count"] = len(resp.Segments)
		if n := len(resp.Segments); n > 0 {
			seconds := int(resp.Segments[n-1].End + 0.5)
			output["duration_seconds"] = resp.Segments[n-1].End
			usage = &langfuse.TokenUsage{
				Output: seconds,
				Total:  seconds,
				Unit:   "SECONDS",
			}
		}
	}
	g.gen.Finish(output, usage, err)
}
