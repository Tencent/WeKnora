package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// toolsServer is the mcpServers contribution the plugin serves itself.
const toolsServer = "tools"

func registerTools(p *pluginsdk.Plugin) {
	p.Tool(toolsServer, pluginapi.Tool{
		Name:  "search_issues",
		Title: "Search Jira issues",
		Description: "Search Jira issues, newest update first. Give jql for precise queries " +
			`(e.g. project = ENG AND status != Done AND assignee = currentUser()), ` +
			"or text to match summaries, descriptions and comments.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{
			"jql":{"type":"string","description":"A JQL query; takes precedence over text and project"},
			"text":{"type":"string","description":"Words to search for"},
			"project":{"type":"string","description":"A project key to limit text search to"},
			"limit":{"type":"integer","minimum":1,"maximum":50,"default":20}}}`),
		Annotations: &pluginapi.ToolAnnotations{ReadOnlyHint: true},
	}, searchIssuesTool)
	p.Tool(toolsServer, pluginapi.Tool{
		Name:        "get_issue",
		Title:       "Read a Jira issue",
		Description: "Read one Jira issue by key (e.g. ENG-123): its facts, description and comments.",
		InputSchema: json.RawMessage(`{"type":"object","required":["key"],"properties":{
			"key":{"type":"string","description":"The issue key"}}}`),
		Annotations: &pluginapi.ToolAnnotations{ReadOnlyHint: true},
	}, getIssueTool)
}

// toolClient connects with the workspace's connection (config.tenant).
func toolClient(ctx context.Context, call *pluginsdk.Call) (*client, error) {
	var cr credentials
	if err := remarshal(call.Config.Tenant, &cr); err != nil {
		return nil, err
	}
	c, err := newClient(ctx, cr, call.Locale)
	if pe, ok := pluginapi.AsError(err); ok && pe.Code == pluginapi.CodeInvalidConfig {
		return nil, fmt.Errorf("%s %s", say(call.Locale, msgToolsNotSetUp), pe.Message)
	}
	return c, err
}

// issueRow is one search result, as the result view shows it.
type issueRow struct {
	Key      string `json:"key"`
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Priority string `json:"priority"`
	Assignee string `json:"assignee"`
	Updated  string `json:"updated"`
	URL      string `json:"url"`
}

func searchIssuesTool(ctx context.Context, call *pluginsdk.Call, args json.RawMessage) (*pluginapi.ToolResult, error) {
	var in struct {
		JQL     string `json:"jql"`
		Text    string `json:"text"`
		Project string `json:"project"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	jql, err := toolJQL(in.JQL, in.Text, in.Project)
	if err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 50 {
		in.Limit = 20
	}
	c, err := toolClient(ctx, call)
	if err != nil {
		return nil, err
	}
	issues, more, err := c.searchPage(ctx, jql, in.Limit)
	if err != nil {
		return nil, err
	}
	rows := make([]issueRow, len(issues))
	var text strings.Builder
	fmt.Fprintf(&text, "%d issue(s) for %s", len(issues), jql)
	if more {
		text.WriteString(" (more exist; narrow the query)")
	}
	text.WriteString("\n")
	for i := range issues {
		f := &issues[i].Fields
		rows[i] = issueRow{
			Key: issues[i].Key, Summary: f.Summary, Status: nameOf(f.Status), Type: nameOf(f.IssueType),
			Priority: nameOf(f.Priority), Assignee: userOf(f.Assignee), Updated: stamp(f.Updated),
			URL: c.issueURL(issues[i].Key),
		}
		r := rows[i]
		fmt.Fprintf(&text, "- %s [%s] %s (%s, %s) %s\n", r.Key, r.Status, r.Summary,
			firstNonEmpty(r.Assignee, "unassigned"), r.Updated, r.URL)
	}
	return pluginapi.StructuredResult(map[string]any{"jql": jql, "issues": rows, "more": more}, text.String()), nil
}

var issueKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-[0-9]+$`)

func getIssueTool(ctx context.Context, call *pluginsdk.Call, args json.RawMessage) (*pluginapi.ToolResult, error) {
	var in struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	if !issueKeyPattern.MatchString(key) {
		return pluginapi.ToolError("%q is not an issue key like ENG-123", in.Key), nil
	}
	c, err := toolClient(ctx, call)
	if err != nil {
		return nil, err
	}
	var is issue
	q := url.Values{"fields": {strings.Join(searchFields, ",")}}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"?"+q.Encode(), nil, &is); err != nil {
		if pe, ok := pluginapi.AsError(err); ok && pe.Code == pluginapi.CodeNotFound {
			return pluginapi.ToolError("issue %s does not exist or is not visible to this account", key), nil
		}
		return nil, err
	}
	md := c.markdown(&is)
	return pluginapi.StructuredResult(map[string]any{"key": is.Key, "url": c.issueURL(is.Key), "markdown": md}, md), nil
}

// toolJQL is the query a search runs: the model's JQL as it is, or a text
// search, newest update first.
func toolJQL(jql, text, project string) (string, error) {
	if jql = strings.TrimSpace(jql); jql != "" {
		if !strings.Contains(strings.ToUpper(jql), "ORDER BY") {
			jql += " ORDER BY updated DESC"
		}
		return jql, nil
	}
	var parts []string
	if p := strings.TrimSpace(project); p != "" {
		parts = append(parts, "project = "+quote(p))
	}
	if t := strings.TrimSpace(text); t != "" {
		parts = append(parts, "text ~ "+quote(t))
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("give jql, or text to search for")
	}
	return strings.Join(parts, " AND ") + " ORDER BY updated DESC", nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
