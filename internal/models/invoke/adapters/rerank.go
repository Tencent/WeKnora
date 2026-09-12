package adapters

// rerank.go — P3 strangler: the rerank facet (design §6.2). This file holds
// the family machinery: the generic (Cohere-style) fallback shape
// (openai/generic/siliconflow/qianfan/gpustack/azure — OpenAI itself serves
// no rerank endpoint), the tolerant result parsing, the shared request
// helper, and the composite adapters. Bespoke vendor wire lives in each
// vendor's file — jina.go, zhipu.go, nvidia.go, aliyun.go, weknoracloud.go
// (HMAC). LKEAP (Tencent TC3) and volcengine (IAM AK/SK) stay on their v1
// SDK clients for now — signature protocols are P5-class native work (task
// ruling 2026-09-10).
//
// Cross-vendor v1 conventions preserved here:
//   - every v1 rerank client (except weknoracloud 60s / volcengine 30s) used
//     newRerankHTTPClient(0) — NO overall timeout — so rerank Builds do NOT
//     set Request.Timeout and ride the executor's chat-kind default;
//   - truncate_prompt_tokens is opt-in ONLY (extra_config, issue #2143);
//   - results are returned in vendor order with (index, score); the echoed
//     document text is re-derived by the caller from the input documents.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

// genericRerankRequest mirrors the v1 fallback wire shape; field order tracks
// the v1 struct for golden parity. additional_data was never populated by v1
// and stays out.
type genericRerankRequest struct {
	Model                string   `json:"model"`
	Query                string   `json:"query"`
	Documents            []string `json:"documents"`
	TruncatePromptTokens int      `json:"truncate_prompt_tokens,omitempty"`
}

// rankResultWire carries the tolerant result parsing v1 centralised in
// RankResult.UnmarshalJSON: relevance_score with a score fallback, and a
// document echoed either as a string or as {"text": ...}.
type rankResultWire struct {
	Index int `json:"index"`
	// Score fills from relevance_score first, then score (fallback helper).
	Score    float64         `json:"-"`
	RawScore json.RawMessage `json:"-"`
}

func (r *rankResultWire) UnmarshalJSON(data []byte) error {
	var temp struct {
		Index          int             `json:"index"`
		RelevanceScore *float64        `json:"relevance_score"`
		Score          *float64        `json:"score"`
		Logit          *float64        `json:"logit"`
		Document       json.RawMessage `json:"document"`
	}
	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal rank result: %w", err)
	}
	r.Index = temp.Index
	switch {
	case temp.RelevanceScore != nil:
		r.Score = *temp.RelevanceScore
	case temp.Score != nil:
		r.Score = *temp.Score
	case temp.Logit != nil:
		r.Score = *temp.Logit
	}
	return nil
}

// rerankResultsWire parses the two v1 result envelopes: openai/jina style
// data-less "results" arrays (tolerant unmarshal above).
func parseRankResults(body []byte, envelope string) ([]invoke.RerankResult, error) {
	var resp struct {
		Results  []rankResultWire `json:"results"`
		Rankings []rankResultWire `json:"rankings"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	wire := resp.Results
	if envelope == "rankings" {
		wire = resp.Rankings
	}
	out := make([]invoke.RerankResult, 0, len(wire))
	for _, r := range wire {
		out = append(out, invoke.RerankResult{Index: r.Index, Score: r.Score})
	}
	return out, nil
}

// buildRerankRequestShared is the JSON POST helper shared by the simple
// rerank shapes (Bearer auth, no explicit timeout — v1 rerank clients ran
// unbounded HTTP clients; see the file header).
func buildRerankRequestShared(
	baseURL string, ep invoke.Endpoint, body any, path string,
) (*invoke.Request, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	return &invoke.Request{
		Method: http.MethodPost,
		URL:    baseURL + path,
		Header: header,
		Body:   data,
	}, nil
}

// --- generic (Cohere-style) fallback (openai, generic, azure_openai,
// siliconflow, qianfan, gpustack — every rerank-shard provider without a
// bespoke shape) ---

func buildGenericRerank(ep invoke.Endpoint, model string, opts *invoke.RerankOptions) (*invoke.Request, error) {
	base := ep.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	body := genericRerankRequest{
		Model:                model,
		Query:                opts.Query,
		Documents:            opts.Documents,
		TruncatePromptTokens: ep.TruncatePromptTokens, // opt-in only (issue #2143)
	}
	return buildRerankRequestShared(base, ep, body, "/rerank")
}

// --- vendor spec + composites ---

type rerankSpec struct {
	build func(ep invoke.Endpoint, model string, opts *invoke.RerankOptions) (*invoke.Request, error)
	parse func(status int, header http.Header, body []byte) (*invoke.RerankResponse, error)
}

// parseResultsEnvelopeRerank reads the top-level results[] envelope. The name
// describes what is shared, not one vendor: the generic fallback and the
// bespoke jina/zhipu/weknoracloud shapes all get the same response body.
func parseResultsEnvelopeRerank(_ int, _ http.Header, body []byte) (*invoke.RerankResponse, error) {
	results, err := parseRankResults(body, "results")
	if err != nil {
		return nil, err
	}
	return &invoke.RerankResponse{Results: results}, nil
}

func rerankSpecFor(name invoke.ProviderName) rerankSpec {
	switch name {
	case invoke.ProviderJina:
		return rerankSpec{build: buildJinaRerank, parse: parseResultsEnvelopeRerank}
	case invoke.ProviderZhipu:
		return rerankSpec{build: buildZhipuRerank, parse: parseResultsEnvelopeRerank}
	case invoke.ProviderNvidia:
		return rerankSpec{build: buildNvidiaRerank, parse: parseNvidiaRerank}
	case invoke.ProviderWeKnoraCloud:
		return rerankSpec{build: buildWeKnoraCloudRerank, parse: parseResultsEnvelopeRerank}
	default:
		return rerankSpec{build: buildGenericRerank, parse: parseResultsEnvelopeRerank}
	}
}

func rerankCapsFor(name invoke.ProviderName) *invoke.RerankCaps {
	info, ok := providerInfoFor(name)
	if !ok {
		return nil
	}
	return info.EffectiveCapabilities().Rerank
}

// openaiRerankAdapter adds the rerank facet to the chat+embedding composite
// (triple-facet vendors).
type openaiRerankAdapter struct {
	openaiEmbeddingAdapter
	rspec rerankSpec
}

var _ invoke.RerankAdapter = (*openaiRerankAdapter)(nil)

// Capabilities unions all three served shards.
func (a *openaiRerankAdapter) Capabilities() invoke.Capabilities {
	caps := a.openaiEmbeddingAdapter.Capabilities()
	caps.Rerank = rerankCapsFor(a.name)
	return caps
}

// BuildRerankRequest dispatches to the vendor build.
func (a *openaiRerankAdapter) BuildRerankRequest(
	ep invoke.Endpoint, model string, opts *invoke.RerankOptions,
) (*invoke.Request, error) {
	return a.rspec.build(ep, model, opts)
}

// ParseRerankResponse dispatches to the vendor parse.
func (a *openaiRerankAdapter) ParseRerankResponse(
	status int, header http.Header, body []byte,
) (*invoke.RerankResponse, error) {
	return a.rspec.parse(status, header, body)
}

// openaiRerankOnlyAdapter serves chat+rerank vendors without an embedding
// shard (none today among the openai family — kept for symmetry with the
// registration switch; lkeap uses its own TC3 adapter).
type openaiRerankOnlyAdapter struct {
	openaiAdapter
	rspec rerankSpec
}

var _ invoke.RerankAdapter = (*openaiRerankOnlyAdapter)(nil)

// Capabilities unions the chat and rerank shards.
func (a *openaiRerankOnlyAdapter) Capabilities() invoke.Capabilities {
	caps := a.openaiAdapter.Capabilities()
	caps.Rerank = rerankCapsFor(a.name)
	return caps
}

// BuildRerankRequest dispatches to the vendor build.
func (a *openaiRerankOnlyAdapter) BuildRerankRequest(
	ep invoke.Endpoint, model string, opts *invoke.RerankOptions,
) (*invoke.Request, error) {
	return a.rspec.build(ep, model, opts)
}

// ParseRerankResponse dispatches to the vendor parse.
func (a *openaiRerankOnlyAdapter) ParseRerankResponse(
	status int, header http.Header, body []byte,
) (*invoke.RerankResponse, error) {
	return a.rspec.parse(status, header, body)
}
