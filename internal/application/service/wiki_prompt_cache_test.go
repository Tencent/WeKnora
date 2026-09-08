package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type wikiCacheCapture struct {
	templateCaptureChatModel
	id, name string
	keys     []string
	retry    bool
}

func (m *wikiCacheCapture) GetModelID() string   { return m.id }
func (m *wikiCacheCapture) GetModelName() string { return m.name }
func (m *wikiCacheCapture) Chat(
	ctx context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.keys = append(m.keys, opts.PromptCacheKey)
	response, err := m.templateCaptureChatModel.Chat(ctx, messages, opts)
	if m.retry && len(m.keys) == 1 {
		return nil, errors.New("API request failed with status 503")
	}
	return response, err
}

func wikiCachePageData() map[string]string {
	return map[string]string{
		"HasAdditions": "1", "SharedSourceContexts": "shared ![source](minio://kb/shared.jpg)",
		"CustomInstructions": "Use precise terminology.", "InstructionScope": "wiki_content",
		"PageSlug": "concept/alpha", "PageTitle": "Alpha", "PageType": "concept", "PageAliases": "A",
		"ExistingContent": "old alpha", "NewContent": "new alpha",
		"AvailableSlugs": "concept/beta", "Language": "English",
	}
}

func TestWikiPromptCacheInvariants(t *testing.T) {
	capture := func(tenant uint64, id, name string, data map[string]string) *wikiCacheCapture {
		t.Helper()
		m := &wikiCacheCapture{id: id, name: name}
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenant)
		_, err := (&wikiIngestService{}).generateWithTemplate(ctx, m, agent.WikiPageModifyUserPrompt, data)
		require.NoError(t, err)
		require.NotEmpty(t, m.options.PromptCacheKey)
		require.Equal(
			t,
			chat.BuildPromptCacheKey(
				tenant, chat.FingerprintPromptPrefix(id, name), "wiki_page_modify", m.prefix,
			),
			m.options.PromptCacheKey,
		)
		return m
	}
	base := capture(7, "config-a", "effective-a", wikiCachePageData())
	for _, tc := range []struct {
		name                string
		tenant              uint64
		id, model           string
		changes             map[string]string
		samePrefix, sameKey bool
	}{
		{
			"page metadata", 7, "config-a", "effective-a",
			map[string]string{
				"PageSlug": "entity/beta", "PageTitle": "Beta", "PageType": "entity",
				"PageAliases": "B", "ExistingContent": "old beta", "NewContent": "new beta",
				"AvailableSlugs": "entity/gamma", "Language": "Chinese",
			},
			true, true,
		},
		{
			"page images", 7, "config-a", "effective-a",
			map[string]string{
				"ExistingContent": "![old](minio://kb/old.jpg)",
				"NewContent":      "![new](minio://kb/new.jpg)",
			},
			true, true,
		},
		{
			"shared context", 7, "config-a", "effective-a",
			map[string]string{"SharedSourceContexts": "different shared source"},
			false, false,
		},
		{"tenant", 8, "config-a", "effective-a", nil, true, false},
		{"model config", 7, "config-b", "effective-a", nil, true, false},
		{"effective model", 7, "config-a", "effective-b", nil, true, false},
		{
			"system instructions", 7, "config-a", "effective-a",
			map[string]string{"CustomInstructions": "Different KB guidance"},
			false, false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := wikiCachePageData()
			for k, v := range tc.changes {
				data[k] = v
			}
			got := capture(tc.tenant, tc.id, tc.model, data)
			require.Equal(t, tc.samePrefix, base.prefix == got.prefix, "prefix identity")
			require.Equal(t, tc.sameKey, base.options.PromptCacheKey == got.options.PromptCacheKey, "provider key")
			if tc.samePrefix {
				require.Equal(
					t, strings.Split(base.messages[1].Content, "<page_metadata>")[0],
					strings.Split(got.messages[1].Content, "<page_metadata>")[0],
				)
			}
		})
	}
}

func TestWikiPromptCacheRetryAndMissingTenant(t *testing.T) {
	m := &wikiCacheCapture{id: "config", name: "effective", retry: true}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	_, err := (&wikiIngestService{}).generateWithTemplate(
		ctx, m, agent.WikiPageModifyUserPrompt, wikiCachePageData(),
	)
	require.NoError(t, err)
	require.Len(t, m.keys, 2)
	require.NotEmpty(t, m.keys[0])
	require.Equal(t, m.keys[0], m.keys[1])
	m = &wikiCacheCapture{id: "config", name: "effective"}
	_, err = (&wikiIngestService{}).generateWithTemplate(
		context.Background(), m, agent.WikiPageModifyUserPrompt, wikiCachePageData(),
	)
	require.NoError(t, err)
	require.Empty(t, m.options.PromptCacheKey)
}

func TestWikiPromptPurposeMapping(t *testing.T) {
	for _, tc := range []struct{ prompt, purpose string }{
		{agent.WikiCandidateSlugPrompt, "wiki_candidate_slug"},
		{agent.WikiKnowledgeExtractPrompt, "wiki_knowledge_extract"},
		{agent.WikiSummaryPrompt, "wiki_summary"},
		{agent.WikiChunkCitationPrompt, "wiki_chunk_citation"},
		{agent.WikiDeduplicationPrompt, "wiki_deduplication"},
		{agent.WikiTaxonomyPlanPrompt, "wiki_taxonomy_plan"},
		{agent.WikiPageModifyUserPrompt, "wiki_page_modify"},
		{agent.WikiIndexIntroPrompt, "wiki_index_intro"},
		{agent.WikiIndexIntroUpdatePrompt, "wiki_index_intro"},
	} {
		t.Run(tc.purpose, func(t *testing.T) {
			m := &templateCaptureChatModel{}
			_, err := (&wikiIngestService{}).generateWithTemplate(
				context.Background(), m, tc.prompt, wikiCachePageData(),
			)
			require.NoError(t, err)
			require.Equal(t, tc.purpose, m.purpose)
			require.NotEmpty(t, m.prefix)
		})
	}
}

func TestWikiDocumentPromptsSharedInstructionsBeforeContent(t *testing.T) {
	for _, prompt := range []string{agent.WikiCandidateSlugPrompt, agent.WikiSummaryPrompt} {
		m := &templateCaptureChatModel{}
		_, err := (&wikiIngestService{}).generateWithTemplate(
			context.Background(), m, prompt,
			map[string]string{
				"Content": "DOCUMENT_SENTINEL", "CustomInstructions": "CUSTOM_SENTINEL",
				"InstructionScope": "wiki_content", "Language": "English", "Granularity": "standard",
				"GranularityGuidance": agent.WikiGranularityGuidance("standard"),
			},
		)
		require.NoError(t, err)
		require.Less(t, strings.Index(m.prompt, "CUSTOM_SENTINEL"), strings.Index(m.prompt, "DOCUMENT_SENTINEL"))
		require.Contains(t, m.prompt, "Apply these business instructions only when they do not conflict")
	}
}

func TestWikiPromptCacheStableImageMaskRoundTrip(t *testing.T) {
	data := wikiCachePageData()
	data["CustomInstructions"] = "Reference ![guide](minio://kb/guide.jpg)"
	data["ExistingContent"] = "![old](minio://kb/old.jpg)"
	data["NewContent"] = "![source](minio://kb/shared.jpg) ![new](minio://kb/new.jpg)"
	masked, urls := maskTemplateDataImageURLs(data, "CustomInstructions", "SharedSourceContexts")
	for field, original := range data {
		require.Equal(t, original, unmaskImageURLs(masked[field], urls), field)
	}
	require.Contains(t, masked["CustomInstructions"], "wkimg:0001")
	require.Contains(t, masked["SharedSourceContexts"], "wkimg:0002")
	require.Contains(t, masked["NewContent"], "wkimg:0002")
}
