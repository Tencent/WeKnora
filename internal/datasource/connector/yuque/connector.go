package yuque

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

// ResolveResourceAncestors has nothing to do for Yuque: repositories are a flat
// list with no nesting, so a selection has no ancestors to reveal.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

// ListResources returns all repos (personal + team) accessible to the token.
// Serial fetch for v1 (user groups typically <10). TODO(perf): parallelize if slow.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	// Yuque resources are a flat list of repositories (no nesting), so a
	// lazy-load request for a specific parent has nothing extra to return.
	if parentID != "" {
		return []types.Resource{}, nil
	}

	cfg, err := parseYuqueConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	me, err := cli.GetCurrentUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}

	repos := make(map[int64]v2Repo)

	// Team token: /api/v2/user returns type="Group" — the token represents a team,
	// not a personal user. In this case, directly list the team's own repos instead
	// of going through the personal-repos + user-groups flow.
	if me.Type == "Group" {
		logger.Infof(ctx, "[Yuque] detected team token (type=Group, login=%s), listing team repos directly", me.Login)
		teamRepos, err := cli.ListGroupRepos(ctx, me.Login)
		if err != nil {
			return nil, fmt.Errorf("list team repos: %w", err)
		}
		for _, r := range teamRepos {
			repos[r.ID] = r
		}
	} else {
		// Personal token flow: list user's own repos + repos from joined groups.
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
			// Yuque returns 404 when the user has not joined any groups (teams),
			// instead of an empty list. Treat this as "no groups" and continue
			// — personal repos were already fetched above.
			logger.Warnf(ctx, "[Yuque] list user groups failed (treating as empty): %v", err)
			groups = nil
		}
		for _, g := range groups {
			teamRepos, err := cli.ListGroupRepos(ctx, g.Login)
			if err != nil {
				// Skip this group but continue others (e.g., 403 on a restricted group).
				logger.Warnf(ctx, "[Yuque] skip group %s: %v", g.Login, err)
				continue
			}
			for _, r := range teamRepos {
				if _, ok := repos[r.ID]; !ok {
					repos[r.ID] = r
				}
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
	settings := parseFolderSettings(ctx, config)
	if settings.FolderMode != folderModeNone {
		logger.Infof(ctx, "[Yuque] folder path derivation enabled: folder_mode=%s toc_only=%t",
			settings.FolderMode, settings.TOCOnly)
	}

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

		// Optionally fetch the book's table of contents and fold it into a
		// per-document folder path. One request per book per sync.
		//
		// tocOK separates "the TOC was fetched and this document is not in it"
		// from "we could not fetch the TOC at all". Both the path assembly and
		// the toc_only filter depend on that distinction: with no TOC we must
		// degrade to the previous flat behaviour rather than silently dropping
		// every document.
		var tocSegs map[int64][]string
		var inTOC map[int64]bool
		tocOK := false
		if settings.FolderMode == folderModeTOC {
			toc, tocErr := cli.ListBookTOC(ctx, bookID)
			if tocErr != nil {
				logger.Warnf(ctx, "[Yuque] book %d: fetch TOC failed, falling back to flat file names: %v",
					bookID, tocErr)
			} else {
				tocSegs, inTOC = buildTOCPaths(toc)
				tocOK = true
			}
		}
		bookName := ""

		currentDocs := make(map[string]bool)
		newCursor.BookDocTimes[bookIDStr] = make(map[string]string)

		var skippedType, skippedDraft, skippedNotInTOC, kept int
		var sampleSkipType, sampleSkipDraft, sampleSkipNotInTOC string
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
			// Mark the document as seen so the deletion detector does not treat a
			// filtered document as removed from the source. toc_only is an
			// admission filter: it decides what enters the knowledge base, not what
			// leaves it. Reporting these as deleted would remove exactly the
			// documents the Yuque web UI cannot show (created through the API,
			// never attached to the TOC), leaving their content unreachable from
			// both sides.
			docIDStr := strconv.FormatInt(d.ID, 10)
			currentDocs[docIDStr] = true

			// Filter out documents that do not appear in the TOC. Applies only when
			// the TOC was actually fetched — see the tocOK comment above. The
			// predicate is membership in the TOC node list, not "has a path":
			// a document may sit in the TOC with no enclosing group.
			if settings.TOCOnly && tocOK && !inTOC[d.ID] {
				skippedNotInTOC++
				if sampleSkipNotInTOC == "" {
					sampleSkipNotInTOC = fmt.Sprintf("id=%d title=%q", d.ID, d.Title)
				}
				continue
			}
			kept++
			// Only admitted documents enter the cursor. A filtered document has to
			// stay unknown to the incremental comparison, so that admitting it
			// later — by attaching it to the TOC, or by switching toc_only off —
			// ingests it rather than dismissing it as unchanged.
			newCursor.BookDocTimes[bookIDStr][docIDStr] = d.ContentUpdatedAt

			// Incremental: skip if content hasn't changed.
			if incremental && prev != nil && prev.BookDocTimes != nil {
				if prevTimes, ok := prev.BookDocTimes[bookIDStr]; ok {
					if prevTimes[docIDStr] == d.ContentUpdatedAt {
						continue
					}
				}
			}

			// Rate-limit: pause between GetDocDetail calls to avoid hitting
			// Yuque's API rate limit (typically ~100 req/5min for personal tokens).
			if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
				return nil, nil, err
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

			// Book title for the leading path segment. The detail response
			// already carries it, so this costs no extra API call. Captured
			// once per book; falls back to the namespace, then to no prefix.
			if bookName == "" {
				bookName = strings.TrimSpace(detail.Book.Name)
				if bookName == "" {
					bookName = strings.TrimSpace(detail.Book.Namespace)
				}
			}

			// folder_path is derived downstream from FileName alone
			// (SplitKnowledgeRelativePath → NormalizeKnowledgeFolderPath), so the
			// hierarchy has to be encoded here as a relative path.
			fileName := buildFolderFileName(settings, tocOK, bookName, tocSegs[d.ID], d.Title)

			out = append(out, types.FetchedItem{
				ExternalID:       docIDStr,
				Title:            d.Title,
				Content:          []byte(detail.Body),
				ContentType:      "text/markdown",
				FileName:         fileName,
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

		logger.Infof(ctx, "[Yuque] book %d: total=%d kept=%d skipped_non_doc=%d skipped_draft=%d "+
			"skipped_not_in_toc=%d toc_loaded=%t non_doc_sample={%s} draft_sample={%s} "+
			"not_in_toc_sample={%s}",
			bookID, len(docs), kept, skippedType, skippedDraft, skippedNotInTOC, tocOK,
			sampleSkipType, sampleSkipDraft, sampleSkipNotInTOC)

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
