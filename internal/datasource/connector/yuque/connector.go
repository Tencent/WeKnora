package yuque

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Compile-time proof that *Connector satisfies the datasource.Connector interface.
// Catches signature drift as soon as the interface changes, rather than at
// container wiring or runtime.
var _ datasource.Connector = (*Connector)(nil)

// Connector implements datasource.Connector for Yuque.
type Connector struct{}

// NewConnector creates a new Yuque connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeYuque }

// Validate verifies the given credentials by pinging the current-user endpoint.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseYuqueConfig(config)
	if err != nil {
		return err
	}
	cli := newClient(cfg)
	if err := cli.Ping(ctx); err != nil {
		return fmt.Errorf("yuque connection failed: %w", err)
	}
	return nil
}

// ListResources returns all repos (personal + team) accessible to the token.
// Serial fetch for v1 (user groups typically <10). TODO(perf): parallelize if slow.
func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig) ([]types.Resource, error) {
	cfg, err := parseYuqueConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	me, err := cli.GetCurrentUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}

	// Personal repos come first and win over team representations of the same repo
	// (first-write-wins dedup) — owner-side metadata is more authoritative.
	repos := make(map[int64]v2Repo)

	personal, err := cli.ListUserRepos(ctx, me.Login)
	if err != nil {
		return nil, fmt.Errorf("list personal repos: %w", err)
	}
	for _, r := range personal {
		if _, ok := repos[r.ID]; !ok {
			repos[r.ID] = r
		}
	}

	groups, err := cli.ListUserGroups(ctx, me.ID)
	if err != nil {
		return nil, fmt.Errorf("list user groups: %w", err)
	}
	for _, g := range groups {
		teamRepos, err := cli.ListGroupRepos(ctx, g.Login)
		if err != nil {
			// Skip this group but continue others (e.g., 403 on a restricted group).
			// Log so operators can distinguish partial failures from empty groups.
			logger.Warnf(ctx, "[Yuque] skip group %s: %v", g.Login, err)
			continue
		}
		for _, r := range teamRepos {
			if _, ok := repos[r.ID]; !ok {
				repos[r.ID] = r
			}
		}
	}

	out := make([]types.Resource, 0, len(repos))
	for _, r := range repos {
		out = append(out, types.Resource{
			ExternalID:  strconv.FormatInt(r.ID, 10),
			Name:        r.Name,
			Type:        "book",
			URL:         cfg.GetBaseURL() + "/" + r.Namespace,
			Description: r.Namespace,
			ModifiedAt:  parseContentUpdatedAt(r.UpdatedAt),
			Metadata: map[string]interface{}{
				"public":    r.Public,
				"book_type": r.Type,
			},
		})
	}
	// Stable, deterministic order for UI rendering and response-body caching.
	sort.Slice(out, func(i, j int) bool { return out[i].ExternalID < out[j].ExternalID })
	return out, nil
}

// FetchAll performs a full sync of all books specified in resourceIDs.
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

// walk is the shared implementation for FetchAll / FetchIncremental.
// If incremental is false, prev is ignored and no cursor is returned (returns nil for cursor).
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *yuqueCursor,
	incremental bool,
) ([]types.FetchedItem, *yuqueCursor, error) {
	cfg, err := parseYuqueConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli := newClient(cfg)

	newCursor := &yuqueCursor{LastSyncTime: time.Now(), BookDocTimes: make(map[string]map[string]string)}
	var out []types.FetchedItem

	for _, bookIDStr := range resourceIDs {
		bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid book id %q: %w", bookIDStr, err)
		}

		docs, err := cli.ListBookDocs(ctx, bookID)
		if err != nil {
			return nil, nil, fmt.Errorf("list docs for book %d: %w", bookID, err)
		}

		currentDocs := make(map[string]bool)
		newCursor.BookDocTimes[bookIDStr] = make(map[string]string)

		var skippedType, skippedDraft, kept int
		var sampleSkipType, sampleSkipDraft string
		for _, d := range docs {
			// Empty type/status is treated as acceptable — forward-compat with
			// API variations that omit the field.
			if d.Type != "" && d.Type != "Doc" {
				skippedType++
				if sampleSkipType == "" {
					sampleSkipType = fmt.Sprintf("id=%d type=%q title=%q", d.ID, d.Type, d.Title)
				}
				continue
			}
			if d.Status != "" && d.Status != "1" {
				skippedDraft++
				if sampleSkipDraft == "" {
					sampleSkipDraft = fmt.Sprintf("id=%d status=%q title=%q", d.ID, d.Status, d.Title)
				}
				continue
			}
			kept++
			docIDStr := strconv.FormatInt(d.ID, 10)
			currentDocs[docIDStr] = true
			newCursor.BookDocTimes[bookIDStr][docIDStr] = d.ContentUpdatedAt

			// Incremental: skip if content hasn't changed.
			if incremental && prev != nil && prev.BookDocTimes != nil {
				if prevTimes, ok := prev.BookDocTimes[bookIDStr]; ok {
					if prevTimes[docIDStr] == d.ContentUpdatedAt {
						continue
					}
				}
			}

			detail, err := cli.GetDocDetail(ctx, d.ID)
			if err != nil {
				// Record failure but continue (placeholder item with error metadata).
				// Keep doc_id/book_id/slug for observability pipelines that join on these.
				out = append(out, types.FetchedItem{
					ExternalID:       docIDStr,
					Title:            d.Title,
					SourceResourceID: bookIDStr,
					Metadata: map[string]string{
						"error":   err.Error(),
						"channel": types.ChannelYuque,
						"doc_id":  docIDStr,
						"book_id": bookIDStr,
						"slug":    d.Slug,
					},
				})
				continue
			}

			// Yuque serializes `body` as Markdown when format is "markdown" or
			// "lake" (Lake XML lives separately in `body_lake`) — empirically
			// verified against the v2 API. Any other format (e.g. "html") may
			// put non-Markdown content in `body`, so skip defensively.
			if detail.Format != "" && detail.Format != "markdown" && detail.Format != "lake" {
				logger.Warnf(ctx, "[Yuque] skip doc %d (%q): unsupported format %q",
					d.ID, d.Title, detail.Format)
				out = append(out, types.FetchedItem{
					ExternalID:       docIDStr,
					Title:            d.Title,
					SourceResourceID: bookIDStr,
					Metadata: map[string]string{
						"channel":     types.ChannelYuque,
						"doc_id":      docIDStr,
						"book_id":     bookIDStr,
						"slug":        d.Slug,
						"skip_reason": "unsupported format: " + detail.Format,
					},
				})
				continue
			}

			out = append(out, types.FetchedItem{
				ExternalID:       docIDStr,
				Title:            d.Title,
				Content:          []byte(detail.Body),
				ContentType:      "text/markdown",
				FileName:         sanitizeFileName(d.Title) + ".md",
				URL:              buildDocURL(cfg.GetBaseURL(), detail.Book.Namespace, d.Slug),
				UpdatedAt:        parseContentUpdatedAt(d.ContentUpdatedAt),
				SourceResourceID: bookIDStr,
				Metadata: map[string]string{
					"doc_id":     docIDStr,
					"book_id":    bookIDStr,
					"slug":       d.Slug,
					"creator":    strconv.FormatInt(d.UserID, 10),
					"word_count": strconv.Itoa(d.WordCount),
					"channel":    types.ChannelYuque,
				},
			})
		}

		logger.Infof(ctx, "[Yuque] book %d: total=%d kept=%d skipped_non_doc=%d skipped_draft=%d non_doc_sample={%s} draft_sample={%s}",
			bookID, len(docs), kept, skippedType, skippedDraft, sampleSkipType, sampleSkipDraft)

		// Deletion detection (incremental only): previous doc IDs not in current → IsDeleted=true
		if incremental && prev != nil && prev.BookDocTimes != nil {
			if prevTimes, ok := prev.BookDocTimes[bookIDStr]; ok {
				for prevDocID := range prevTimes {
					if !currentDocs[prevDocID] {
						out = append(out, types.FetchedItem{
							ExternalID:       prevDocID,
							IsDeleted:        true,
							SourceResourceID: bookIDStr,
						})
					}
				}
			}
		}
	}

	if !incremental {
		return out, nil, nil
	}
	return out, newCursor, nil
}

// buildDocURL constructs a browser URL for a Yuque doc.
// Namespace may be empty on some responses; fall back to base URL only.
func buildDocURL(baseURL, namespace, slug string) string {
	if namespace == "" {
		return baseURL
	}
	return baseURL + "/" + namespace + "/" + slug
}

// FetchIncremental returns items changed (or deleted) since the prior cursor.
// Deletion detection: docs present in the prior cursor but absent from the
// current list are emitted as IsDeleted=true placeholder items.
func (c *Connector) FetchIncremental(
	ctx context.Context,
	config *types.DataSourceConfig,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	resourceIDs := config.ResourceIDs
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource IDs (book IDs) configured")
	}

	// Decode prior cursor (if any).
	var prev *yuqueCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		var p yuqueCursor
		b, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(b, &p)
		prev = &p
	}

	items, newCursor, err := c.walk(ctx, config, resourceIDs, prev, true)
	if err != nil {
		return nil, nil, err
	}

	// Marshal newCursor into a generic map for the SyncCursor wrapper.
	cursorMap := make(map[string]interface{})
	b, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(b, &cursorMap)

	return items, &types.SyncCursor{
		LastSyncTime:    newCursor.LastSyncTime,
		ConnectorCursor: cursorMap,
	}, nil
}
