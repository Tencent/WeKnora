package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// searchConversationsMaxResults bounds how much conversation history one call
// can pull into the context window. Past conversations are verbose and the
// agent is usually looking for one exchange, not a reading list.
const searchConversationsMaxResults = 8

// searchConversationsSnippetRunes truncates each side of a recalled exchange.
const searchConversationsSnippetRunes = 400

var searchConversationsTool = BaseTool{
	name: ToolSearchConversations,
	description: `Search this user's own past conversations with the assistant.

## When to Use

Use this tool when the user refers to something that was discussed before but is
not in the current conversation:
- "上次你给我的那个配置" / "we talked about this last month"
- "我之前问过的那个报错" — the error and its answer are in an older session
- The user assumes shared context that this session does not contain

Do not use when:
- The answer is in documents (use knowledge_search — that is the knowledge base)
- The information is in the current conversation already
- The user is asking a general question with no reference to the past

## What It Returns

Matching exchanges from the user's own previous sessions, each with the session
title, the date, the user's question and the assistant's answer.

## Notes

- Only this user's own conversations are searched, never a colleague's.
- Past answers may be outdated. Prefer current documents when they disagree,
  and say so rather than repeating a stale answer as fact.`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "What to look for in past conversations, in the user's own words"
    },
    "limit": {
      "type": "integer",
      "description": "Maximum number of past exchanges to return (default 5, max 8)"
    }
  },
  "required": ["query"]
}`),
}

// SearchConversationsInput defines the input parameters for the tool.
type SearchConversationsInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// SearchConversationsTool lets the agent look things up in the user's own chat
// history.
//
// A long-term memory feature that distils conversations into a few dozen
// sentences will always lose detail — the exact config someone was given three
// weeks ago is not a durable fact about them, and storing it would be the wrong
// shape. Keeping the transcripts searchable and letting the agent go back for
// them is the same division of labour MemGPT uses: a small always-present
// summary plus retrieval into the full history on demand.
type SearchConversationsTool struct {
	BaseTool
	messageService interfaces.MessageService
	// ownerID is the person whose history may be searched. It is captured when
	// the tool is built rather than read from the model's arguments, so no
	// prompt can talk the agent into reading someone else's conversations.
	ownerID string
	// currentSessionID is excluded from results: the model already has this
	// conversation, and returning it wastes context and invites loops.
	currentSessionID string
}

// NewSearchConversationsTool creates the conversation history search tool.
func NewSearchConversationsTool(
	messageService interfaces.MessageService, ownerID, currentSessionID string,
) *SearchConversationsTool {
	return &SearchConversationsTool{
		BaseTool:         searchConversationsTool,
		messageService:   messageService,
		ownerID:          ownerID,
		currentSessionID: currentSessionID,
	}
}

// Execute searches the user's own past conversations.
func (t *SearchConversationsTool) Execute(
	ctx context.Context, args json.RawMessage,
) (*types.ToolResult, error) {
	var input SearchConversationsInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "query is required",
		}, fmt.Errorf("missing query")
	}
	if t.messageService == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "conversation history search is not available",
		}, fmt.Errorf("no message service")
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > searchConversationsMaxResults {
		limit = searchConversationsMaxResults
	}

	result, err := t.messageService.SearchMessages(ctx, &types.MessageSearchParams{
		Query: query,
		Mode:  types.MessageSearchModeHybrid,
		// Over-fetch so dropping the current session cannot empty the result.
		Limit:   limit + 2,
		OwnerID: t.ownerID,
	})
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Conversation search failed: %v", err),
		}, err
	}

	var b strings.Builder
	b.WriteString("<past_conversations>\n")
	found := 0
	for _, item := range result.Items {
		if item == nil || found >= limit {
			continue
		}
		if item.SessionID == t.currentSessionID {
			continue
		}
		found++
		fmt.Fprintf(&b, "<exchange session=\"%s\" date=\"%s\">\n",
			xmlEscape(item.SessionTitle), item.CreatedAt.Format("2006-01-02"))
		if question := snippet(item.QueryContent, searchConversationsSnippetRunes); question != "" {
			fmt.Fprintf(&b, "<user>%s</user>\n", xmlEscape(question))
		}
		if answer := snippet(item.AnswerContent, searchConversationsSnippetRunes); answer != "" {
			fmt.Fprintf(&b, "<assistant>%s</assistant>\n", xmlEscape(answer))
		}
		b.WriteString("</exchange>\n")
	}
	b.WriteString("</past_conversations>")

	if found == 0 {
		return &types.ToolResult{
			Success: true,
			Output: "<past_conversations />\n" +
				"Nothing in this user's past conversations matches. " +
				"Do not assume it was discussed before.",
			Data: map[string]interface{}{"query": query, "matches": 0},
		}, nil
	}

	return &types.ToolResult{
		Success: true,
		Output:  b.String(),
		Data:    map[string]interface{}{"query": query, "matches": found},
	}, nil
}

// snippet trims and truncates one side of an exchange.
func snippet(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "…"
}
