package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Agent inbox tools (agent-sidebar-inbox P1 MVP).
//
// These tools implement the "pull, never push" context contract: the inbox
// stores curated KB/user/agent events, and the agent reads them only when it
// decides to — nothing is ever injected into the prompt. See
// works/agent-sidebar-inbox/plan.md §4.4 and §4.6.

// inboxCheckMaxSinceMinutes bounds the lookback window of check_inbox.
const inboxCheckMaxSinceMinutes = 24 * 60

// inboxCheckMaxLimit bounds how many events one check may return.
const inboxCheckMaxLimit = 50

// CheckInboxTool surfaces the situational inbox to the agent.
type CheckInboxTool struct {
	BaseTool
	inbox interfaces.AgentInboxService
	kbIDs []string
}

// NewCheckInboxTool creates the check_inbox tool scoped to the session's KBs.
func NewCheckInboxTool(inbox interfaces.AgentInboxService, kbIDs []string) *CheckInboxTool {
	return &CheckInboxTool{
		BaseTool: NewBaseTool(
			ToolCheckInbox,
			"Read the situation inbox: recent user actions (pages viewed, documents "+
				"uploaded, edits, errors), knowledge-base backend events (parse completed/failed) "+
				"and your own past write receipts. Check it when the user's request is vague "+
				"(e.g. 'help me', 'what happened'), before acting on pages, or when asked about "+
				"system state. Nothing here is otherwise visible to you.",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"since_minutes": {
						"type": "integer",
						"description": "Look back this many minutes (default 30, max 1440)."
					},
					"types": {
						"type": "array",
						"items": {"type": "string"},
						"description": "Optional filter, e.g. [\"user.viewed_page\",\"parse.failed\"]."
					},
					"limit": {
						"type": "integer",
						"description": "Max events to return (default 20, max 50)."
					},
					"mark_read": {
						"type": "boolean",
						"description": "Mark returned events as read (default true)."
					}
				},
				"required": []
			}`),
		),
		inbox: inbox,
		kbIDs: kbIDs,
	}
}

// inboxScope resolves tenant/owner/KB scope from the tool execution context.
// TenantID comes from the execution tenant; the owner from the originating
// session so one user can never read another's action trail.
func inboxScope(ctx context.Context, kbIDs []string) (types.InboxQueryFilters, bool) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return types.InboxQueryFilters{}, false
	}
	f := types.InboxQueryFilters{
		TenantID: tenantID,
		KBIDs:    kbIDs,
		Limit:    20,
	}
	if owner := types.SessionOwnerIDFromContext(ctx); owner != "" {
		f.UserID = owner
	}
	return f, true
}

// Execute queries the inbox digest inside the window and optionally marks
// the returned events read.
func (t *CheckInboxTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	if t.inbox == nil {
		return &types.ToolResult{Success: false, Error: "inbox is not available"}, nil
	}
	scope, ok := inboxScope(ctx, t.kbIDs)
	if !ok {
		return &types.ToolResult{Success: false, Error: "no tenant context for inbox"}, nil
	}

	var params struct {
		SinceMinutes int      `json:"since_minutes"`
		Types        []string `json:"types"`
		Limit        int      `json:"limit"`
		MarkRead     *bool    `json:"mark_read"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
		}
	}
	if params.SinceMinutes <= 0 {
		params.SinceMinutes = 30
	}
	if params.SinceMinutes > inboxCheckMaxSinceMinutes {
		params.SinceMinutes = inboxCheckMaxSinceMinutes
	}
	if params.Limit <= 0 {
		params.Limit = 20
	}
	if params.Limit > inboxCheckMaxLimit {
		params.Limit = inboxCheckMaxLimit
	}
	markRead := params.MarkRead == nil || *params.MarkRead

	since := time.Now().Add(-time.Duration(params.SinceMinutes) * time.Minute)
	scope.Types = params.Types
	scope.Since = &since
	scope.Limit = params.Limit

	digests, remaining, err := t.inbox.Check(ctx, scope, markRead)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "inbox query failed: " + err.Error()}, nil
	}
	if len(digests) == 0 {
		msg := "Inbox is empty for the requested window."
		if remaining > 0 {
			msg = fmt.Sprintf(
				"No events in the requested window; %d older unread events exist "+
					"(raise since_minutes to see them).", remaining)
		}
		return &types.ToolResult{Success: true, Output: msg}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d event(s), newest first:", len(digests))
	for _, d := range digests {
		fmt.Fprintf(&sb, "\n- [%s] %s %s", d.Priority, d.Time, d.Type)
		if len(d.Payload) > 0 {
			if brief, err := json.Marshal(d.Payload); err == nil && len(brief) > 0 {
				fmt.Fprintf(&sb, " %s", brief)
			}
		}
	}
	fmt.Fprintf(&sb, "\n\n%d unread event(s) remain.", remaining)
	return &types.ToolResult{Success: true, Output: sb.String()}, nil
}

// WikiGetCurrentPageTool resolves "the page the user is looking at" from the
// latest view event, then reads the page on demand. The pointer travels, the
// content does not — that is the whole point.
type WikiGetCurrentPageTool struct {
	BaseTool
	inbox     interfaces.AgentInboxService
	pageSvc   interfaces.WikiPageService
	knowledge interfaces.KnowledgeService
	kbIDs     []string
}

// NewWikiGetCurrentPageTool creates wiki_get_current_page.
func NewWikiGetCurrentPageTool(
	inbox interfaces.AgentInboxService,
	pageSvc interfaces.WikiPageService,
	knowledge interfaces.KnowledgeService,
	kbIDs []string,
) *WikiGetCurrentPageTool {
	return &WikiGetCurrentPageTool{
		BaseTool: NewBaseTool(
			ToolWikiGetCurrentPage,
			"Identify which wiki page the user is currently viewing and read it on "+
				"demand. Returns slug, title, summary, numbered heading outline (use the "+
				"numbers to resolve references like 'the 5th section') and, optionally, the "+
				"full content. Always call this when the user says 'this page' or refers to "+
				"a section without naming the page.",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"include_content": {
						"type": "boolean",
						"description": "Include the full page markdown (default false: only the outline)."
					}
				},
				"required": []
			}`),
		),
		inbox:     inbox,
		pageSvc:   pageSvc,
		knowledge: knowledge,
		kbIDs:     kbIDs,
	}
}

// renderHeadingsNumbers parses markdown headings in document order and
// renders a numbered outline (N. level marker + text). The ordinal is the
// stable reference the agent echoes back in edit instructions.
func renderHeadingsNumbers(content string) string {
	var sb strings.Builder
	n := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		if level > 6 {
			continue
		}
		text := strings.TrimSpace(strings.TrimLeft(trimmed, "# "))
		if text == "" {
			continue
		}
		n++
		fmt.Fprintf(&sb, "%d. %s %s\n", n, strings.Repeat("#", level), text)
	}
	return sb.String()
}

// Execute resolves the current-page pointer from the latest view event and
// renders the live page outline (plus optional full content) on demand.
func (t *WikiGetCurrentPageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	if t.inbox == nil || t.pageSvc == nil {
		return &types.ToolResult{Success: false, Error: "current-page lookup is not available"}, nil
	}
	scope, ok := inboxScope(ctx, t.kbIDs)
	if !ok {
		return &types.ToolResult{Success: false, Error: "no tenant context for current-page lookup"}, nil
	}

	var params struct {
		IncludeContent bool `json:"include_content"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
		}
	}

	evt, err := t.inbox.LatestView(ctx, scope)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "view lookup failed: " + err.Error()}, nil
	}
	if evt == nil {
		msg := "No page-view signal yet: the user has not opened any wiki page " +
			"since the session started. Ask which page they mean, or use wiki_search."
		return &types.ToolResult{Success: true, Output: msg}, nil
	}
	slug, _ := evt.Payload["slug"].(string)
	title, _ := evt.Payload["title"].(string)
	if slug == "" {
		msg := "The latest view event has no slug (the user may be on a non-wiki " +
			"tab). Ask which page they mean."
		return &types.ToolResult{Success: true, Output: msg}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Current page (from view signal %s):\nslug: %s\ntitle: %s\n",
		evt.CreatedAt.Format(time.RFC3339), slug, title)

	// Prefer the live page over the event snapshot: the signal may be stale
	// if the user lingered. The event's title is the fallback when the page
	// was renamed or removed since.
	for _, kbID := range t.kbIDs {
		page, err := t.pageSvc.GetPageBySlug(ctx, kbID, slug)
		if err != nil || page == nil {
			continue
		}
		if page.Title != "" {
			fmt.Fprintf(&sb, "live title: %s\n", page.Title)
		}
		fmt.Fprintf(&sb,
			"page_type: %s\nsummary: %s\n\nHeading outline (numbered, for resolving 'the Nth section'):\n%s",
			page.PageType, page.Summary, renderHeadingsNumbers(page.Content))
		if params.IncludeContent {
			fmt.Fprintf(&sb, "\nFull content:\n%s", page.Content)
		}
		return &types.ToolResult{Success: true, Output: sb.String()}, nil
	}
	fmt.Fprintf(&sb, "\nThe page no longer exists (deleted or renamed since the view). Use wiki_search to relocate it.")
	return &types.ToolResult{Success: true, Output: sb.String()}, nil
}

// WikiGetPageTrailTool returns the recent view history so "the page I was on
// before" resolves without the user repeating themselves.
type WikiGetPageTrailTool struct {
	BaseTool
	inbox interfaces.AgentInboxService
	kbIDs []string
}

// NewWikiGetPageTrailTool creates wiki_get_page_trail.
func NewWikiGetPageTrailTool(inbox interfaces.AgentInboxService, kbIDs []string) *WikiGetPageTrailTool {
	return &WikiGetPageTrailTool{
		BaseTool: NewBaseTool(
			ToolWikiGetPageTrail,
			"List the pages the user recently viewed in chronological order. Use it to "+
				"resolve references like 'the page I was on before' or 'those two pages'.",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"limit": {
						"type": "integer",
						"description": "Max trail entries (default 10, max 50)."
					}
				},
				"required": []
			}`),
		),
		inbox: inbox,
		kbIDs: kbIDs,
	}
}

// Execute renders the recent page-view trail in chronological order.
func (t *WikiGetPageTrailTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	if t.inbox == nil {
		return &types.ToolResult{Success: false, Error: "page trail is not available"}, nil
	}
	scope, ok := inboxScope(ctx, t.kbIDs)
	if !ok {
		return &types.ToolResult{Success: false, Error: "no tenant context for page trail"}, nil
	}
	var params struct {
		Limit int `json:"limit"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
		}
	}
	rows, err := t.inbox.Trail(ctx, scope, params.Limit)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "trail query failed: " + err.Error()}, nil
	}
	if len(rows) == 0 {
		return &types.ToolResult{Success: true, Output: "No page views recorded yet."}, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Page trail (oldest first, %d entries):", len(rows))
	for i, row := range rows {
		slug, _ := row.Payload["slug"].(string)
		title, _ := row.Payload["title"].(string)
		fmt.Fprintf(&sb, "\n%d. %s  %s (%s)", i+1, row.CreatedAt.Format("15:04:05"), title, slug)
	}
	return &types.ToolResult{Success: true, Output: sb.String()}, nil
}
