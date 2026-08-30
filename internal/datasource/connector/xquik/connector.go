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
var _ datasource.ScheduledFullSyncResumer = (*Connector)(nil)

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

// ShouldResumeScheduledFullSync reports whether a full-sync schedule must
// finish an existing Xquik snapshot before starting another one.
func (c *Connector) ShouldResumeScheduledFullSync(cursor *types.SyncCursor) bool {
	return decodeCursor(cursor).InProgress
}

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

queriesLoop:
	for len(state.PendingQueries) > 0 {
		current := state.PendingQueries[0]
		for state.ResultsFetched < cfg.ResultsPerQuery {
			remaining := cfg.ResultsPerQuery - state.ResultsFetched
			page, err := client.search(ctx, searchRequest{
				Query:     current.Query,
				Cursor:    current.PageCursor,
				SinceTime: state.SinceTime,
				UntilTime: state.StartedAt,
				Limit:     remaining,
			})
			if err != nil {
				return nil, fmt.Errorf("query %q: %w", current.Query, err)
			}
			if len(page.Tweets) > remaining {
				return nil, fmt.Errorf("query %q returned more posts than requested", current.Query)
			}
			state.PagesFetched++

			for _, post := range page.Tweets {
				if state.ResultsFetched >= cfg.ResultsPerQuery {
					break
				}
				id := strings.TrimSpace(post.ID)
				if !validPostID(id) {
					return nil, fmt.Errorf("query %q returned a post with an invalid id", current.Query)
				}
				state.ResultsFetched++
				if _, duplicate := seen[id]; duplicate {
					continue
				}
				item := c.postItem(post, current.Query, state.StartedAt)
				if err := handler.Emit(ctx, item); err != nil {
					return nil, err
				}
				rememberPostID(&state, seen, id, maxPersistedPostIDs)
			}

			if !page.hasMore() {
				state.PendingQueries = state.PendingQueries[1:]
				state.ResultsFetched = 0
				state.PagesFetched = 0
				cursor, err := checkpointQueryTransition(ctx, &state, handler)
				if err != nil {
					return nil, err
				}
				if cursor != nil {
					return cursor, nil
				}
				continue queriesLoop
			}
			nextCursor, err := validatedNextCursor(current.Query, current.PageCursor, page)
			if err != nil {
				return nil, err
			}
			state.PendingQueries[0].PageCursor = nextCursor
			if state.ResultsFetched >= cfg.ResultsPerQuery || state.PagesFetched >= maxPagesPerQuery {
				state.DeferredQueries = append(state.DeferredQueries, state.PendingQueries[0])
				state.PendingQueries = state.PendingQueries[1:]
				state.ResultsFetched = 0
				state.PagesFetched = 0
				cursor, err := checkpointQueryTransition(ctx, &state, handler)
				if err != nil {
					return nil, err
				}
				if cursor != nil {
					return cursor, nil
				}
				continue queriesLoop
			}
			if err := handler.Checkpoint(ctx, connectorCursor(state, false)); err != nil {
				return nil, err
			}
			current.PageCursor = nextCursor
		}
	}

	return connectorCursor(state, true), nil
}

func checkpointQueryTransition(
	ctx context.Context,
	state *cursorState,
	handler datasource.StreamHandler,
) (*types.SyncCursor, error) {
	if len(state.PendingQueries) == 0 {
		if len(state.DeferredQueries) == 0 {
			return connectorCursor(*state, true), nil
		}
		state.PendingQueries = state.DeferredQueries
		state.DeferredQueries = nil
		cursor := connectorCursor(*state, false)
		return cursor, handler.Checkpoint(ctx, cursor)
	}
	return nil, handler.Checkpoint(ctx, connectorCursor(*state, false))
}

func rememberPostID(state *cursorState, seen map[string]struct{}, id string, limit int) {
	if len(state.SeenPostIDs) < limit {
		state.SeenPostIDs = append(state.SeenPostIDs, id)
		seen[id] = struct{}{}
		return
	}
	evicted := state.SeenPostIDs[state.SeenPostOffset]
	delete(seen, evicted)
	state.SeenPostIDs[state.SeenPostOffset] = id
	state.SeenPostOffset = (state.SeenPostOffset + 1) % limit
	seen[id] = struct{}{}
}

func validatedNextCursor(query string, current string, page searchPage) (string, error) {
	next := strings.TrimSpace(page.nextCursor())
	if next == "" {
		return "", fmt.Errorf("query %q reported another page without a cursor", query)
	}
	if next == current {
		return "", fmt.Errorf("query %q repeated its cursor", query)
	}
	if len(next) > maxPageCursorBytes {
		return "", fmt.Errorf("query %q returned an oversized cursor", query)
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
		PendingQueries: queryQueue(queries),
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
