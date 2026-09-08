package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type wikiCacheProbeSample struct {
	chunks       string
	expectedSlug string
}

var wikiCacheProbeSamples = []wikiCacheProbeSample{
	{`<c id="c001">WeKnora 是一个用于知识检索和问答的开源系统。</c>`, "entity/weknora"},
	{`<c id="c002">Embedding 缓存会复用相同文本已经生成的向量。</c>`, "concept/embedding-cache"},
	{`<c id="c003">回归检查在 Recall 或 MRR 绝对下降超过 0.03 时失败。</c>`, "concept/regression-check"},
	{`<c id="c004">正式基线必须由人工检查真实评测结果后确认。</c>`, "concept/formal-baseline"},
}

const wikiCacheProbeCandidates = `[
  {"type":"entity","name":"WeKnora","slug":"entity/weknora","aliases":[],"description":"知识检索和问答系统"},
  {"type":"concept","name":"Embedding 缓存","slug":"concept/embedding-cache","aliases":[],"description":"复用文本向量"},
  {"type":"concept","name":"回归检查","slug":"concept/regression-check","aliases":[],"description":"检测检索质量下降"},
  {"type":"concept","name":"正式基线","slug":"concept/formal-baseline","aliases":[],"description":"人工确认的真实评测基准"}
]`

func buildWikiCacheProbePrompt(layout string, sample int) (string, string, string, error) {
	if layout != "legacy" && layout != "optimized" {
		return "", "", "", fmt.Errorf("layout must be legacy or optimized")
	}
	if sample < 0 || sample >= len(wikiCacheProbeSamples) {
		return "", "", "", fmt.Errorf("sample must be between 0 and %d", len(wikiCacheProbeSamples)-1)
	}
	selected := wikiCacheProbeSamples[sample]
	tmpl, err := template.New("topic3-wiki-cache-probe").Parse(agent.WikiChunkCitationPrompt)
	if err != nil {
		return "", "", "", fmt.Errorf("parse wiki prompt: %w", err)
	}
	var rendered strings.Builder
	data := map[string]string{
		"Language":       "Chinese",
		"CandidateSlugs": wikiCacheProbeCandidates,
		"ChunksXML":      selected.chunks,
	}
	if err := tmpl.Execute(&rendered, data); err != nil {
		return "", "", "", fmt.Errorf("render wiki prompt: %w", err)
	}
	optimized := rendered.String()
	chunkBlock := "<chunks>\n" + selected.chunks + "\n</chunks>"
	prompt := optimized
	stablePrefix := optimized
	if index := strings.Index(optimized, "<chunks>"); index >= 0 {
		stablePrefix = optimized[:index]
	}
	if layout == "legacy" {
		withoutDynamicBlock := strings.Replace(optimized, chunkBlock, "", 1)
		prompt = chunkBlock + "\n\n" + withoutDynamicBlock
		stablePrefix = "<chunks>\n"
	}
	return prompt, stablePrefix, selected.expectedSlug, nil
}

func validateWikiCacheProbeOutput(content, expectedSlug string) (bool, bool) {
	clean := strings.TrimSpace(content)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(strings.TrimSpace(clean), "```")
	var result struct {
		Citations map[string][]string `json:"citations"`
		NewSlugs  []any               `json:"new_slugs"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(clean)), &result) != nil {
		return false, false
	}
	_, found := result.Citations[expectedSlug]
	return true, found
}

func (e *EvaluationService) WikiCacheProbe(ctx context.Context, chatModelID, layout string, sample int) (*types.WikiCacheProbeResult, error) {
	prompt, stablePrefix, expectedSlug, err := buildWikiCacheProbePrompt(layout, sample)
	if err != nil {
		return nil, err
	}
	model, err := e.modelService.GetChatModel(ctx, chatModelID)
	if err != nil {
		return nil, fmt.Errorf("get chat model: %w", err)
	}
	purpose := "topic3_wiki_cache_" + layout
	fingerprint := chat.FingerprintPromptPrefix(stablePrefix)
	ctx = types.WithLLMCallMetadata(ctx, purpose, fingerprint)
	thinking := false
	options := &chat.ChatOptions{Temperature: 0.3, MaxTokens: 384, MaxCompletionTokens: 384, Thinking: &thinking}
	started := time.Now()
	response, err := model.Chat(ctx, []chat.Message{{Role: "user", Content: prompt}}, options)
	if err != nil {
		return nil, fmt.Errorf("wiki cache probe: %w", err)
	}
	if response == nil {
		return nil, fmt.Errorf("wiki cache probe returned no response")
	}
	valid, found := validateWikiCacheProbeOutput(response.Content, expectedSlug)
	return &types.WikiCacheProbeResult{
		Layout: layout, Sample: sample, Purpose: purpose, PrefixFingerprint: fingerprint,
		ExpectedSlug: expectedSlug, Output: response.Content, OutputValid: valid,
		ExpectedFound: found, Usage: response.Usage, DurationMS: time.Since(started).Milliseconds(),
	}, nil
}
