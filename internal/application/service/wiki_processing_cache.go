package service

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
)

type wikiProcessingKBKey struct{}

var errWikiMapCacheAdmission = errors.New("wiki map result cannot be cached")

func sortedWikiCacheItems(items []extractedItem) []extractedItem {
	out := append([]extractedItem(nil), items...)
	for i := range out {
		out[i].Aliases = append([]string(nil), out[i].Aliases...)
		sort.Strings(out[i].Aliases)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := json.Marshal(out[i])
		b, _ := json.Marshal(out[j])
		return string(a) < string(b)
	})
	return out
}

func withWikiProcessingScope(ctx context.Context, document, kb string) context.Context {
	return context.WithValue(withProcessingDocument(ctx, document), wikiProcessingKBKey{}, kb)
}

func wikiProcessingKB(ctx context.Context) string {
	v, _ := ctx.Value(wikiProcessingKBKey{}).(string)
	return v
}

func (s *wikiIngestService) wikiMapCacheEnabled(ctx context.Context, model chat.Chat) bool {
	tenant, _ := types.TenantIDFromContext(ctx)
	return s.processingCache.Enabled() && tenant != 0 && processingDocument(ctx) != "" && wikiProcessingKB(ctx) != "" &&
		processingModelIdentity(model) != "" &&
		!processingCacheBypassed(ctx)
}

// Historical slugs are live state. Cached extraction asks only about content;
// current exact/semantic identity reconciliation still runs after every replay.
func (s *wikiIngestService) wikiPreviousSlugInput(ctx context.Context, model chat.Chat, old map[string]bool) string {
	if s.wikiMapCacheEnabled(ctx, model) {
		return "(none — content-only extraction; existing page identities are reconciled separately)"
	}
	slugs := make([]string, 0, len(old))
	for slug := range old {
		if strings.HasPrefix(slug, "entity/") || strings.HasPrefix(slug, "concept/") {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	if len(slugs) == 0 {
		return "(none — this is a new document)"
	}
	return "- " + strings.Join(slugs, "\n- ") + "\n"
}

type wikiMapCachePayload struct {
	Kind       string
	Extraction *combinedExtraction
	Citation   *citationBatchResult
	Merges     map[string]string
	Summary    string
}

func wikiMapCacheStage(prompt string) string {
	switch prompt {
	case agent.WikiCandidateSlugPrompt:
		return "wiki.map.candidates.v1"
	case agent.WikiKnowledgeExtractPrompt:
		return "wiki.map.extraction.v1"
	case agent.WikiChunkCitationPrompt:
		return "wiki.map.citations.v1"
	case agent.WikiDeduplicationPrompt:
		return "wiki.map.dedup.v1"
	case agent.WikiSummaryPrompt:
		return "wiki.map.summary.v1"
	}
	return ""
}

var wikiCacheHandlePattern = regexp.MustCompile(`^c[0-9]{3,}$`)

func validateWikiMapPayload(v wikiMapCachePayload, stage string, data map[string]string) error {
	if v.Kind != stage {
		return fmt.Errorf("wiki map payload stage mismatch")
	}
	switch stage {
	case "wiki.map.candidates.v1", "wiki.map.extraction.v1":
		if v.Extraction == nil || v.Extraction.Entities == nil || v.Extraction.Concepts == nil {
			return fmt.Errorf("null wiki extraction")
		}
		check := func(items []extractedItem, prefix string) error {
			for _, item := range items {
				if item.Name == "" || !strings.HasPrefix(item.Slug, prefix) || len(item.SourceChunks) > 0 {
					return fmt.Errorf("invalid or bound extracted wiki item")
				}
			}
			return nil
		}
		if err := check(v.Extraction.Entities, "entity/"); err != nil {
			return err
		}
		return check(v.Extraction.Concepts, "concept/")
	case "wiki.map.citations.v1":
		if v.Citation == nil || v.Citation.Citations == nil {
			return fmt.Errorf("null wiki citations")
		}
		check := func(handles []string) error {
			for _, h := range handles {
				if !wikiCacheHandlePattern.MatchString(h) || !strings.Contains(data["ChunksXML"], `id="`+h+`"`) {
					return fmt.Errorf("invalid wiki citation handle %q", h)
				}
			}
			return nil
		}
		for slug, handles := range v.Citation.Citations {
			if slug == "" {
				return fmt.Errorf("empty citation slug")
			}
			if err := check(handles); err != nil {
				return err
			}
		}
		for _, item := range v.Citation.NewSlugs {
			if item.Slug == "" || item.Name == "" {
				return fmt.Errorf("invalid new citation slug")
			}
			if err := check(item.SourceChunks); err != nil {
				return err
			}
		}
	case "wiki.map.dedup.v1":
		if v.Merges == nil {
			return fmt.Errorf("null wiki merges")
		}
	case "wiki.map.summary.v1":
		if strings.TrimSpace(v.Summary) == "" {
			return fmt.Errorf("empty wiki summary")
		}
	default:
		return fmt.Errorf("unsupported wiki cache stage")
	}
	return nil
}

func parseWikiMapPayload(stage string, result wikiTemplateResult, data map[string]string) (wikiMapCachePayload, error) {
	v := wikiMapCachePayload{Kind: stage}
	if err := completeProcessingResponse(
		&types.ChatResponse{Content: result.Content, FinishReason: result.FinishReason},
	); err != nil {
		return v, err
	}
	raw := []byte(cleanLLMJSON(result.Content))
	var err error
	switch stage {
	case "wiki.map.candidates.v1", "wiki.map.extraction.v1":
		err = json.Unmarshal(raw, &v.Extraction)
	case "wiki.map.citations.v1":
		err = json.Unmarshal(raw, &v.Citation)
	case "wiki.map.dedup.v1":
		var parsed struct {
			Merges map[string]string `json:"merges"`
		}
		err = json.Unmarshal(raw, &parsed)
		v.Merges = parsed.Merges
		if err == nil && v.Merges != nil {
			filterWikiCachedMerges(v.Merges, data["Candidates"])
		}
	case "wiki.map.summary.v1":
		v.Summary = result.Content
	}
	if err != nil {
		return v, err
	}
	return v, validateWikiMapPayload(v, stage, data)
}

func filterWikiCachedMerges(merges map[string]string, candidates string) {
	var groups struct {
		Items []struct {
			Slug       string `xml:"slug,attr"`
			Candidates struct {
				Pages []struct {
					Slug string `xml:"slug,attr"`
				} `xml:"page"`
			} `xml:"candidates"`
		} `xml:"item"`
	}
	if xml.Unmarshal([]byte("<root>"+candidates+"</root>"), &groups) != nil {
		for k := range merges {
			delete(merges, k)
		}
		return
	}
	allowed := map[string]map[string]bool{}
	for _, g := range groups.Items {
		allowed[g.Slug] = map[string]bool{}
		for _, p := range g.Candidates.Pages {
			allowed[g.Slug][p.Slug] = true
		}
	}
	for src, dst := range merges {
		if dedupMergeRejectReason(src, dst, allowed[src]) != "" {
			delete(merges, src)
		}
	}
}

func renderWikiMapPayload(v wikiMapCachePayload) (wikiTemplateResult, error) {
	var content []byte
	var err error
	switch v.Kind {
	case "wiki.map.candidates.v1", "wiki.map.extraction.v1":
		content, err = json.Marshal(v.Extraction)
	case "wiki.map.citations.v1":
		content, err = json.Marshal(v.Citation)
	case "wiki.map.dedup.v1":
		content, err = json.Marshal(struct {
			Merges map[string]string `json:"merges"`
		}{v.Merges})
	case "wiki.map.summary.v1":
		content = []byte(v.Summary)
	}
	return wikiTemplateResult{Content: string(content), FinishReason: "stop"}, err
}

func (s *wikiIngestService) cachedWikiMapResult(
	ctx context.Context,
	model chat.Chat,
	prompt string,
	messages []chat.Message,
	options *chat.ChatOptions,
	data map[string]string,
	compute func() (interface{}, error),
) (interface{}, error) {
	stage := wikiMapCacheStage(prompt)
	if stage == "" || !s.wikiMapCacheEnabled(ctx, model) {
		return compute()
	}
	tenant, _ := types.TenantIDFromContext(ctx)
	var original wikiTemplateResult
	computed := false
	value, err := processingcache.Value(ctx, s.processingCache, tenant, processingDocument(ctx), stage, struct {
		Model, KB string
		Messages  []chat.Message
		Options   *chat.ChatOptions
	}{processingModelIdentity(model), wikiProcessingKB(ctx), messages, options}, func(v wikiMapCachePayload) error {
		return validateWikiMapPayload(v, stage, data)
	}, func(_ context.Context) (wikiMapCachePayload, error) {
		raw, err := compute()
		if err != nil {
			return wikiMapCachePayload{}, err
		}
		result, ok := raw.(wikiTemplateResult)
		if !ok {
			return wikiMapCachePayload{}, fmt.Errorf("invalid wiki result")
		}
		original, computed = result, true
		parsed, err := parseWikiMapPayload(stage, result, data)
		if err != nil {
			return parsed, fmt.Errorf("%w: %v", errWikiMapCacheAdmission, err)
		}
		return parsed, nil
	})
	if ctx.Err() != nil {
		return wikiTemplateResult{}, ctx.Err()
	}
	if errors.Is(err, errWikiMapCacheAdmission) {
		if computed {
			return original, nil
		}
		value, computeErr := compute()
		if ctx.Err() != nil {
			return wikiTemplateResult{}, ctx.Err()
		}
		return value, computeErr
	}
	if err != nil {
		return wikiTemplateResult{}, err
	}
	return renderWikiMapPayload(value)
}
