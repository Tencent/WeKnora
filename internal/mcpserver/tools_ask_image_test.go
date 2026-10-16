package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestSummarizeReferencesPreservesImagesOutsideExcerpt(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	refs := summarizeReferences([]*types.SearchResult{
		nil,
		{
			ID: "chunk", KnowledgeID: "doc", Content: strings.Repeat("长", 400),
			ImageInfo: `[{"url":"` + ref + `","caption":"diagram","original_url":"private-source"},` +
				`{"url":"","original_url":"private-only"}]`,
		},
		{ID: "chunk", KnowledgeID: "doc", ImageInfo: `[{"url":"` + ref + `","ocr_text":"details"}]`},
		{ID: "broken", ImageInfo: `malformed`},
	})
	require.Len(t, refs, 2)
	require.Equal(t, strings.Repeat("长", askExcerptMaxRunes)+"…", refs[0].Excerpt)
	require.Equal(t, []askImage{{URL: ref, Caption: "diagram", OCRText: "details"}}, refs[0].Images)
	require.Empty(t, refs[1].Images)

	cut := summarizeReferences([]*types.SearchResult{{
		ID: "cut", Content: strings.Repeat("x", askExcerptMaxRunes-10) + "![](" + ref + ")",
	}})
	require.Equal(t, strings.Repeat("x", askExcerptMaxRunes-10)+"![](…", cut[0].Excerpt,
		"a reference crossing the limit is dropped, never halved")
	raw, err := json.Marshal(refs)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-source")
	require.NotContains(t, string(raw), "private-only")
}

type imageAskSessionService struct {
	interfaces.SessionService
	refs []*types.SearchResult
}

func (s *imageAskSessionService) CreateSession(_ context.Context, session *types.Session) (*types.Session, error) {
	session.ID = "session-1"
	return session, nil
}

func (s *imageAskSessionService) SetSessionOwnerID(context.Context, uint64, string, string) error {
	return nil
}

func (s *imageAskSessionService) KnowledgeQA(ctx context.Context, _ *types.QARequest, bus *event.EventBus) error {
	if err := bus.EmitAndWait(ctx, event.Event{
		Type: event.EventAgentReferences, Data: event.AgentReferencesData{References: s.refs},
	}); err != nil {
		return err
	}
	return bus.EmitAndWait(ctx, event.Event{
		Type: event.EventAgentFinalAnswer,
		Data: event.AgentFinalAnswerData{Content: "A textual answer without a Markdown image.", Done: true},
	})
}

type imageAskMessageService struct{ interfaces.MessageService }

func (s *imageAskMessageService) CreateMessage(_ context.Context, message *types.Message) (*types.Message, error) {
	message.ID = "message-" + message.Role
	return message, nil
}

func (s *imageAskMessageService) UpdateMessage(context.Context, *types.Message) error { return nil }

func TestAskExposesImageHandlesToTextAndStructuredClients(t *testing.T) {
	f := newResourceURLFixture(t)
	const publicURL = "https://cdn.example/img.png?a=1&b=2"
	info, err := json.Marshal([]types.ImageInfo{{
		URL: f.ref, Caption: "chart", OCRText: "values", OriginalURL: "private-parser-locator",
	}, {URL: publicURL}})
	require.NoError(t, err)
	f.srv.sessionService = &imageAskSessionService{refs: []*types.SearchResult{{
		ID: "chunk", KnowledgeID: "doc-1", Content: strings.Repeat("long text ", 100), ImageInfo: string(info),
	}}}
	f.srv.messageService = &imageAskMessageService{}
	f.srv.agentService = &stubAgentService{agents: map[string]*types.CustomAgent{
		types.BuiltinQuickAnswerID: {ID: types.BuiltinQuickAnswerID, IsBuiltin: true},
	}}
	result, err := f.srv.handleAsk(f.ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Arguments: map[string]any{"question": "Explain the diagram"},
	}})
	require.NoError(t, err)
	require.False(t, result.IsError, "%#v", result.Content)
	require.Contains(t, result.Content[0].(mcp.TextContent).Text, `Image: "`+f.ref+`"`)
	require.Contains(t, result.Content[0].(mcp.TextContent).Text, `Image: "`+publicURL+`"`)
	result = f.rewrite(result)
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var parsed struct {
		References []askReference `json:"references"`
	}
	require.NoError(t, json.Unmarshal(raw, &parsed))
	require.Len(t, parsed.References, 1)
	require.Len(t, parsed.References[0].Images, 2)
	require.Equal(t, publicURL, parsed.References[0].Images[1].URL)
	img := parsed.References[0].Images[0]
	require.True(t, strings.HasPrefix(img.URL, "https://weknora.example/prefix/r/"))
	require.Equal(t, "chart", img.Caption)
	require.Equal(t, "values", img.OCRText)
	require.Contains(t, result.Content[0].(mcp.TextContent).Text, img.URL)
	require.NotContains(t, string(raw), "private-parser-locator")
	require.NotContains(t, string(raw), "resource://")
}
