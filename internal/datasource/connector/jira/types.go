package jira

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	editionCloud  = "cloud"
	editionServer = "server"
)

type config struct {
	edition         string
	baseURL         string
	username        string
	secret          string
	jql             string
	includeComments bool
}

func (c config) cloud() bool { return c.edition == editionCloud }

func configValue(ds *types.DataSourceConfig, name string) string {
	if ds == nil {
		return ""
	}
	if v, ok := ds.Credentials[name].(string); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	if v, ok := ds.Settings[name].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func configBool(ds *types.DataSourceConfig, name string, defaultVal bool) bool {
	if ds == nil {
		return defaultVal
	}
	if v, ok := ds.Settings[name].(bool); ok {
		return v
	}
	if v, ok := ds.Credentials[name].(bool); ok {
		return v
	}
	strVal := strings.ToLower(configValue(ds, name))
	if strVal == "false" || strVal == "0" {
		return false
	}
	if strVal == "true" || strVal == "1" {
		return true
	}
	return defaultVal
}

func parseConfig(ds *types.DataSourceConfig) (config, error) {
	if ds == nil {
		return config{}, datasource.ErrInvalidConfig
	}
	cfg := config{
		edition:         strings.ToLower(configValue(ds, "edition")),
		baseURL:         strings.TrimRight(configValue(ds, "base_url"), "/"),
		username:        configValue(ds, "username"),
		jql:             configValue(ds, "jql"),
		includeComments: configBool(ds, "include_comments", true),
	}
	if cfg.edition == "" {
		cfg.edition = editionServer
	}
	if cfg.edition != editionServer && cfg.edition != editionCloud {
		return config{}, fmt.Errorf("%w: unsupported edition %q", datasource.ErrInvalidCredentials, cfg.edition)
	}
	if cfg.username == "" {
		return config{}, fmt.Errorf("%w: base_url and username are required", datasource.ErrInvalidCredentials)
	}
	normalized, err := normalizeBaseURL(cfg.baseURL)
	if err != nil {
		return config{}, err
	}
	cfg.baseURL = normalized

	if cfg.cloud() {
		cfg.secret = configValue(ds, "api_token")
	} else {
		cfg.secret = configValue(ds, "password")
	}
	if cfg.secret == "" {
		return config{}, fmt.Errorf("%w: credentials are required", datasource.ErrInvalidCredentials)
	}
	return cfg, nil
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(strings.TrimRight(raw, "/"))
	if raw == "" {
		return "", fmt.Errorf("%w: base_url and username are required", datasource.ErrInvalidCredentials)
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "", fmt.Errorf("%w: invalid base_url", datasource.ErrInvalidCredentials)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

type project struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
	Self string `json:"self"`
}

type searchResponse struct {
	StartAt    int     `json:"startAt"`
	MaxResults int     `json:"maxResults"`
	Total      int     `json:"total"`
	Issues     []issue `json:"issues"`
	NextPageToken string `json:"nextPageToken,omitempty"`
	IsLast       bool   `json:"isLast,omitempty"`
}

type issue struct {
	ID             string          `json:"id"`
	Key            string          `json:"key"`
	Self           string          `json:"self"`
	Fields         issueFields     `json:"fields"`
	RenderedFields *renderedFields `json:"renderedFields,omitempty"`
}

type issueFields struct {
	Summary     string                   `json:"summary"`
	Description interface{}              `json:"description"`
	IssueType   struct{ Name string `json:"name"` } `json:"issuetype"`
	Status      struct{ Name string `json:"name"` } `json:"status"`
	Priority    *struct{ Name string `json:"name"` } `json:"priority,omitempty"`
	Resolution  *struct{ Name string `json:"name"` } `json:"resolution,omitempty"`
	Assignee    *struct{ DisplayName string `json:"displayName"` } `json:"assignee,omitempty"`
	Reporter    *struct{ DisplayName string `json:"displayName"` } `json:"reporter,omitempty"`
	Created     string                   `json:"created"`
	Updated     string                   `json:"updated"`
	Components  []struct{ Name string `json:"name"` } `json:"components"`
	Labels      []string                 `json:"labels"`
	FixVersions []struct{ Name string `json:"name"` } `json:"fixVersions"`
	Comment     *commentList             `json:"comment,omitempty"`
	Attachment  []attachment             `json:"attachment,omitempty"`
}

type attachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	Author      *struct{ DisplayName string `json:"displayName"` } `json:"author,omitempty"`
	Created     string `json:"created"`
	Size        int64  `json:"size"`
	MimeType    string `json:"mimeType"`
	Content     string `json:"content"`
	TextContent string `json:"-"`
}

const (
	maxAttachmentBytes = 50 * 1024 * 1024 // 50MB max download size
	maxEmbeddedBytes   = 64 * 1024        // 64KB max embedded text in Markdown
)

// embeddableTextExts maps file extensions to markdown code block language identifiers.
var embeddableTextExts = map[string]string{
	".log":   "log",
	".txt":   "text",
	".text":  "text",
	".json":  "json",
	".yaml":  "yaml",
	".yml":   "yaml",
	".xml":   "xml",
	".csv":   "csv",
	".sql":   "sql",
	".sh":    "bash",
	".bash":  "bash",
	".py":    "python",
	".java":  "java",
	".go":    "go",
	".js":    "javascript",
	".ts":    "typescript",
	".md":    "markdown",
	".html":  "html",
	".css":   "css",
	".diff":  "diff",
	".patch": "diff",
}

type commentList struct {
	StartAt    int       `json:"startAt"`
	MaxResults int       `json:"maxResults"`
	Total      int       `json:"total"`
	Comments   []comment `json:"comments"`
}

type comment struct {
	ID      string `json:"id"`
	Author  *struct{ DisplayName string `json:"displayName"` } `json:"author,omitempty"`
	Created string `json:"created"`
	Updated string `json:"updated"`
	Body    interface{} `json:"body"`
}

type renderedFields struct {
	Description string               `json:"description"`
	Comment     *renderedCommentList `json:"comment,omitempty"`
}

type renderedCommentList struct {
	Comments []renderedComment `json:"comments"`
}

type renderedComment struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

// cursor records sync progress and known issues per project.
type cursor struct {
	ProjectIssues    map[string]map[string]string `json:"project_issues"`
	FullSyncBaseline map[string]map[string]string `json:"full_sync_baseline,omitempty"`
	FullSync         bool                         `json:"full_sync,omitempty"`
	LatestUpdated    map[string]string            `json:"latest_updated,omitempty"`
}

func decodeCursor(old *types.SyncCursor) cursor {
	c := cursor{
		ProjectIssues: map[string]map[string]string{},
		LatestUpdated: map[string]string{},
	}
	if old == nil {
		return c
	}
	raw, _ := json.Marshal(old.ConnectorCursor)
	_ = json.Unmarshal(raw, &c)
	if c.ProjectIssues == nil {
		c.ProjectIssues = map[string]map[string]string{}
	}
	if c.LatestUpdated == nil {
		c.LatestUpdated = map[string]string{}
	}
	return c
}

func cloneIssueMap(in map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(in))
	for res, issues := range in {
		out[res] = make(map[string]string, len(issues))
		for k, v := range issues {
			out[res][k] = v
		}
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (c cursor) clone() cursor {
	return cursor{
		ProjectIssues:    cloneIssueMap(c.ProjectIssues),
		FullSyncBaseline: cloneIssueMap(c.FullSyncBaseline),
		FullSync:         c.FullSync,
		LatestUpdated:    cloneStringMap(c.LatestUpdated),
	}
}

func (c cursor) syncCursor() *types.SyncCursor {
	raw, _ := json.Marshal(c)
	fields := map[string]interface{}{}
	_ = json.Unmarshal(raw, &fields)
	return &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: fields}
}

func prepareSyncCursors(old *types.SyncCursor, forceFull bool) (baseline, next cursor) {
	previous := decodeCursor(old)
	if !forceFull {
		next = previous.clone()
		next.FullSync = false
		next.FullSyncBaseline = nil
		return previous, next
	}
	next = previous.clone()
	if !next.FullSync {
		next.FullSyncBaseline = cloneIssueMap(previous.ProjectIssues)
		next.ProjectIssues = map[string]map[string]string{}
		next.LatestUpdated = map[string]string{}
		next.FullSync = true
	}
	baseline.ProjectIssues = next.FullSyncBaseline
	if baseline.ProjectIssues == nil {
		baseline.ProjectIssues = map[string]map[string]string{}
	}
	return baseline, next
}

func issueFileName(key, summary string) string {
	base := safeFilename(fmt.Sprintf("[%s] %s", key, summary))
	return base + ".md"
}

func safeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\\|?*`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" {
		return "untitled"
	}
	runes := []rune(name)
	if len(runes) > 200 {
		return string(runes[:200])
	}
	return name
}

// parseJiraTime parses Jira ISO-8601/RFC-3339 timestamps (e.g. 2024-01-15T10:00:00.000+0000 or 2024-01-15T10:00:00.000Z).
func parseJiraTime(str string) time.Time {
	if str == "" {
		return time.Time{}
	}
	formats := []string{
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05.000Z0700",
		"2006-01-02T15:04:05.000Z07:00",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, str); err == nil {
			return t
		}
	}
	return time.Time{}
}
