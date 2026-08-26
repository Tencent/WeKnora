package xquik

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const maxPagesPerQuery = 100

var _ datasource.StreamingConnector = (*Connector)(nil)

// Connector imports public X posts returned by saved Xquik search queries.
type Connector struct {
	apiFactory func(string) api
	now        func() time.Time
}

// NewConnector creates an Xquik data source connector.
func NewConnector() *Connector {
	return &Connector{
		apiFactory: func(apiKey string) api { return newClient(apiKey) },
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeXquik }

// Validate checks the connector settings and API key.
func (c *Connector) Validate(ctx context.Context, ds *types.DataSourceConfig) error {
	cfg, err := parseConfig(ds)
	if err != nil {
		return err
	}
	return c.apiFactory(cfg.APIKey).validate(ctx)
}

// ListResources returns one selectable resource per saved search query.
func (c *Connector) ListResources(
	_ context.Context,
	ds *types.DataSourceConfig,
	parentID string,
) ([]types.Resource, error) {
	if parentID != "" {
		return []types.Resource{}, nil
	}
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	resources := make([]types.Resource, 0, len(cfg.Queries))
	for _, query := range cfg.Queries {
		resources = append(resources, types.Resource{
			ExternalID:  query,
			Name:        query,
			Type:        "search_query",
			Description: "Import matching public X posts through Xquik",
		})
	}
	return resources, nil
}

// ResolveResourceAncestors returns no ancestors because Xquik resources are flat.
func (c *Connector) ResolveResourceAncestors(
	context.Context,
	*types.DataSourceConfig,
	[]string,
) ([]string, error) {
	return []string{}, nil
}

// FetchAll imports the selected search queries from a fresh snapshot.
func (c *Connector) FetchAll(
	ctx context.Context,
	ds *types.DataSourceConfig,
	resourceIDs []string,
) ([]types.FetchedItem, error) {
	collector := &itemCollector{}
	_, err := c.fetch(ctx, ds, resourceIDs, nil, collector)
	return collector.items, err
}

// FetchIncremental imports posts created since the last successful snapshot.
func (c *Connector) FetchIncremental(
	ctx context.Context,
	ds *types.DataSourceConfig,
	old *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	collector := &itemCollector{}
	next, err := c.fetch(ctx, ds, ds.ResourceIDs, old, collector)
	return collector.items, next, err
}

// FetchStream emits posts and resumable cursor checkpoints as pages complete.
func (c *Connector) FetchStream(
	ctx context.Context,
	ds *types.DataSourceConfig,
	old *types.SyncCursor,
	handler datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.fetch(ctx, ds, ds.ResourceIDs, old, handler)
}

func (c *Connector) fetch(
	ctx context.Context,
	ds *types.DataSourceConfig,
	resourceIDs []string,
	old *types.SyncCursor,
	handler datasource.StreamHandler,
) (*types.SyncCursor, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	queries, err := cfg.selectedQueries(resourceIDs)
	if err != nil {
		return nil, err
	}
	state := c.startState(old, queries, cfg.ResultsPerQuery)
	client := c.apiFactory(cfg.APIKey)
	seen := make(map[string]struct{}, len(state.SeenPostIDs))
	for _, id := range state.SeenPostIDs {
		seen[id] = struct{}{}
	}

	for queryIndex := state.QueryIndex; queryIndex < len(queries); queryIndex++ {
		query := queries[queryIndex]
		pageCursor := ""
		resultsFetched := 0
		pagesFetched := 0
		if state.InProgress && queryIndex == state.QueryIndex && state.Query == query {
			pageCursor = state.PageCursor
			resultsFetched = state.ResultsFetched
			pagesFetched = state.PagesFetched
		}

		for resultsFetched < cfg.ResultsPerQuery {
			if pagesFetched >= maxPagesPerQuery {
				return nil, fmt.Errorf("Xquik query exceeded %d cursor pages", maxPagesPerQuery)
			}
			remaining := cfg.ResultsPerQuery - resultsFetched
			page, err := client.search(ctx, searchRequest{
				Query:     query,
				Cursor:    pageCursor,
				SinceTime: state.SinceTime,
				UntilTime: state.StartedAt,
				Limit:     remaining,
			})
			if err != nil {
				return nil, fmt.Errorf("query %q: %w", query, err)
			}
			if len(page.Tweets) > remaining {
				return nil, fmt.Errorf("query %q returned more posts than requested", query)
			}
			pagesFetched++

			for _, post := range page.Tweets {
				if resultsFetched >= cfg.ResultsPerQuery {
					break
				}
				id := strings.TrimSpace(post.ID)
				if !validPostID(id) {
					return nil, fmt.Errorf("query %q returned a post with an invalid id", query)
				}
				resultsFetched++
				if _, duplicate := seen[id]; duplicate {
					continue
				}
				item := c.postItem(post, query, state.StartedAt)
				if err := handler.Emit(ctx, item); err != nil {
					return nil, err
				}
				seen[id] = struct{}{}
				state.SeenPostIDs = append(state.SeenPostIDs, id)
			}

			if !page.hasMore() {
				break
			}
			nextCursor, err := validatedNextCursor(query, pageCursor, page)
			if err != nil {
				return nil, err
			}
			if resultsFetched >= cfg.ResultsPerQuery {
				continuation := state
				continuation.QueryIndex = queryIndex
				continuation.Query = query
				continuation.PageCursor = nextCursor
				continuation.ResultsFetched = 0
				continuation.PagesFetched = pagesFetched
				cursor := connectorCursor(continuation, false)
				if err := handler.Checkpoint(ctx, cursor); err != nil {
					return nil, err
				}
				return cursor, nil
			}
			pageCursor = nextCursor
			checkpoint := state
			checkpoint.QueryIndex = queryIndex
			checkpoint.Query = query
			checkpoint.PageCursor = pageCursor
			checkpoint.ResultsFetched = resultsFetched
			checkpoint.PagesFetched = pagesFetched
			if err := handler.Checkpoint(ctx, connectorCursor(checkpoint, false)); err != nil {
				return nil, err
			}
		}

		checkpoint := state
		checkpoint.QueryIndex = queryIndex + 1
		checkpoint.Query = ""
		checkpoint.PageCursor = ""
		checkpoint.ResultsFetched = 0
		checkpoint.PagesFetched = 0
		if err := handler.Checkpoint(ctx, connectorCursor(checkpoint, false)); err != nil {
			return nil, err
		}
	}

	return connectorCursor(state, true), nil
}

func validatedNextCursor(query string, current string, page searchPage) (string, error) {
	next := strings.TrimSpace(page.nextCursor())
	if next == "" {
		return "", fmt.Errorf("query %q reported another page without a cursor", query)
	}
	if next == current {
		return "", fmt.Errorf("query %q repeated its cursor", query)
	}
	return next, nil
}

func (c *Connector) startState(
	old *types.SyncCursor,
	queries []string,
	resultsPerQuery int,
) cursorState {
	state := decodeCursor(old)
	if state.canResume(queries, resultsPerQuery) {
		return state
	}

	now := c.now().UTC()
	previous := time.Time{}
	if old != nil {
		previous = old.LastSyncTime.UTC()
	}
	since := time.Time{}
	if !previous.IsZero() {
		since = previous.Add(-syncOverlap)
	}
	return cursorState{
		InProgress:     true,
		StartedAt:      now,
		PreviousSyncAt: previous,
		SinceTime:      since,
		QueryListHash:  queryListHash(queries),
	}
}

func (c *Connector) postItem(post tweet, query string, fallbackTime time.Time) types.FetchedItem {
	id := strings.TrimSpace(post.ID)
	handle := post.Author.handle()
	postURL := "https://x.com/i/web/status/" + url.PathEscape(id)
	if validXHandle(handle) {
		postURL = "https://x.com/" + url.PathEscape(handle) + "/status/" + url.PathEscape(id)
	}
	createdAt := post.createdTime()
	if createdAt.IsZero() {
		createdAt = fallbackTime
	}
	title := postTitle(post)
	content := postMarkdown(post, postURL, createdAt)
	metadata := map[string]string{
		"channel":           types.ChannelXquik,
		"source_type":       types.ConnectorTypeXquik,
		"xquik_query":       query,
		"x_post_id":         id,
		"x_author_id":       strings.TrimSpace(post.Author.ID),
		"x_author_username": handle,
		"created_at":        createdAt.Format(time.RFC3339Nano),
		"language":          strings.TrimSpace(post.Language),
		"likes":             strconv.FormatInt(firstNonZero(post.LikeCount, post.LikeCountSnake), 10),
		"reposts":           strconv.FormatInt(firstNonZero(post.RetweetCount, post.RetweetCountSnake), 10),
		"replies":           strconv.FormatInt(firstNonZero(post.ReplyCount, post.ReplyCountSnake), 10),
		"quotes":            strconv.FormatInt(firstNonZero(post.QuoteCount, post.QuoteCountSnake), 10),
		"views":             strconv.FormatInt(firstNonZero(post.ViewCount, post.ViewCountSnake), 10),
		"bookmarks":         strconv.FormatInt(firstNonZero(post.BookmarkCount, post.BookmarkCountSnake), 10),
	}
	return types.FetchedItem{
		ExternalID:       "xquik:post:" + id,
		Title:            title,
		Content:          []byte(content),
		ContentType:      "text/markdown",
		FileName:         "x-post-" + id + ".md",
		URL:              postURL,
		UpdatedAt:        createdAt,
		Metadata:         metadata,
		SourceResourceID: query,
	}
}

func postTitle(post tweet) string {
	handle := post.Author.handle()
	prefix := singleLine(post.Author.Name)
	if handle != "" {
		if prefix != "" {
			prefix += " (@" + handle + ")"
		} else {
			prefix = "@" + handle
		}
	}
	if prefix == "" {
		prefix = "X post"
	}
	text := strings.TrimSpace(strings.ReplaceAll(post.Text, "\n", " "))
	text = truncateRunes(text, 96)
	if text == "" {
		return prefix
	}
	return prefix + ": " + text
}

func postMarkdown(post tweet, postURL string, createdAt time.Time) string {
	authorName := singleLine(post.Author.Name)
	handle := post.Author.handle()
	authorLabel := authorName
	if handle != "" {
		if authorLabel != "" {
			authorLabel += " (@" + handle + ")"
		} else {
			authorLabel = "@" + handle
		}
	}
	if authorLabel == "" {
		authorLabel = "Unknown author"
	}

	var body strings.Builder
	body.WriteString("# Post by ")
	body.WriteString(authorLabel)
	body.WriteString("\n\n")
	text := strings.TrimSpace(strings.ReplaceAll(post.Text, "\x00", ""))
	if text == "" {
		text = "Post text unavailable."
	}
	for _, line := range strings.Split(text, "\n") {
		body.WriteString("> ")
		body.WriteString(line)
		body.WriteByte('\n')
	}
	body.WriteString("\nPublished: ")
	body.WriteString(createdAt.Format(time.RFC3339))
	body.WriteString("\n\nSource: [View post on X](")
	body.WriteString(postURL)
	body.WriteString(")\n")
	return body.String()
}

func singleLine(value string) string {
	value = strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f {
			return ' '
		}
		return char
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func validPostID(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func validXHandle(value string) bool {
	if value == "" || len(value) > 15 {
		return false
	}
	for _, char := range value {
		isDigit := char >= '0' && char <= '9'
		isLowercase := char >= 'a' && char <= 'z'
		isUppercase := char >= 'A' && char <= 'Z'
		if !isDigit && !isLowercase && !isUppercase && char != '_' {
			return false
		}
	}
	return true
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

type itemCollector struct {
	items []types.FetchedItem
}

func (c *itemCollector) Emit(_ context.Context, item types.FetchedItem) error {
	c.items = append(c.items, item)
	return nil
}

func (c *itemCollector) Checkpoint(context.Context, *types.SyncCursor) error { return nil }
