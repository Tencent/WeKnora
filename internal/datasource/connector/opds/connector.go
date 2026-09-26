package opds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// Compile-time proofs that *Connector satisfies the datasource interfaces.
var (
	_ datasource.Connector          = (*Connector)(nil)
	_ datasource.StreamingConnector = (*Connector)(nil)
)

// maxNavigationDepth bounds how many navigation hops are followed below a
// selected feed. 1 means "the selected feed plus one level of sub-feeds": a
// configured root catalog has its direct sections explored, but the tree is
// never walked arbitrarily deep. Deeper content is reached by selecting a
// specific section in the resource picker.
const maxNavigationDepth = 1

// Connector implements datasource.Connector for OPDS catalogs.
type Connector struct{}

// NewConnector creates a new OPDS connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeOPDS }

// Validate verifies that at least one configured catalog URL is reachable and
// parses as an OPDS/Atom feed. Requiring only one success means a multi-catalog
// source still validates while a transiently broken catalog is reported later
// as a partial sync failure.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	cli := newClient(cfg)

	var lastErr error
	for _, catalogURL := range cfg.catalogURLList() {
		if _, err := cli.fetchFeed(ctx, catalogURL); err != nil {
			lastErr = fmt.Errorf("fetch catalog %s: %w", catalogURL, err)
			logger.Warnf(ctx, "[OPDS] validate: %v", lastErr)
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no catalog URL configured", datasource.ErrInvalidCredentials)
	}
	return lastErr
}

// ResolveResourceAncestors has nothing to do: the picker tree is only two
// levels deep and a configured catalog is always a visible root, so a
// selection has no hidden ancestors to reveal.
func (c *Connector) ResolveResourceAncestors(
	context.Context, *types.DataSourceConfig, []string,
) ([]string, error) {
	return []string{}, nil
}

// ListResources returns the selectable resources.
//
//   - parentID == "" → one resource per configured catalog URL.
//   - parentID != "" → that feed's direct children: sections (navigation
//     entries) and books (acquisition entries), one level down.
//
// A feed that fails to fetch still appears, carrying the error in its
// Description, so one broken section does not fail the whole listing and the
// user can deselect it instead.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg)

	if parentID != "" {
		return c.listChildren(ctx, cli, parentID)
	}

	catalogURLs := cfg.catalogURLList()
	out := make([]types.Resource, 0, len(catalogURLs))
	for _, catalogURL := range catalogURLs {
		res := types.Resource{
			ExternalID:  catalogURL,
			Type:        "catalog",
			Name:        catalogURL,
			URL:         catalogURL,
			HasChildren: true,
		}
		feed, err := cli.fetchFeed(ctx, catalogURL)
		if err != nil {
			logger.Warnf(ctx, "[OPDS] list: fetch %s failed: %v", catalogURL, err)
			res.Description = "fetch failed: " + err.Error()
			out = append(out, res)
			continue
		}
		if title := strings.TrimSpace(feed.Title); title != "" {
			res.Name = title
		}
		res.Metadata = map[string]interface{}{
			"kind":       feedKindOf(feed).String(),
			"item_count": len(feed.Entries),
		}
		out = append(out, res)
	}
	return out, nil
}

// listChildren returns the direct children of one feed.
func (c *Connector) listChildren(
	ctx context.Context, cli *client, feedURL string,
) ([]types.Resource, error) {
	feed, err := cli.fetchFeed(ctx, feedURL)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog %s: %w", feedURL, err)
	}
	base, _ := url.Parse(feedURL)

	out := make([]types.Resource, 0, len(feed.Entries))
	for i := range feed.Entries {
		entry := &feed.Entries[i]

		if nav := navigationLink(entry); nav != nil {
			href, err := resolveHref(base, nav.Href)
			if err != nil {
				logger.Warnf(ctx, "[OPDS] list: entry %q: %v", entry.Title, err)
				continue
			}
			out = append(out, types.Resource{
				ExternalID:  href,
				Type:        "navigation",
				Name:        firstNonEmpty(entry.Title, href),
				Description: strings.TrimSpace(entry.Summary),
				URL:         href,
				ParentID:    feedURL,
				// Sections are advertised as expandable; the frontend collapses
				// a node whose children come back empty, so a leaf self-corrects.
				HasChildren: true,
			})
			continue
		}

		if acq := acquisitionLink(entry); acq != nil {
			href, err := resolveHref(base, acq.Href)
			if err != nil {
				logger.Warnf(ctx, "[OPDS] list: entry %q: %v", entry.Title, err)
				continue
			}
			out = append(out, types.Resource{
				ExternalID:  itemExternalID(feedURL, entryID(entry)),
				Type:        "book",
				Name:        firstNonEmpty(entry.Title, href),
				Description: strings.TrimSpace(entry.Summary),
				URL:         href,
				ParentID:    feedURL,
				HasChildren: false,
			})
		}
	}
	return out, nil
}

// FetchAll performs a full sync of the selected feeds (or every configured
// catalog when resourceIDs is empty), returning all items in memory.
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	var collected []types.FetchedItem
	_, err := c.walk(ctx, config, resourceIDs, nil, false, func(item types.FetchedItem) error {
		collected = append(collected, item)
		return nil
	})
	return collected, err
}

// FetchIncremental returns only entries whose content changed since the prior
// cursor. Deletions are intentionally not emitted: a book removed from a
// catalog is simply no longer offered, which is not the same as the user
// deleting it from the knowledge base.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	prev := decodeCursor(ctx, cursor)

	var collected []types.FetchedItem
	newCursor, err := c.walk(ctx, config, config.ResourceIDs, prev, true,
		func(item types.FetchedItem) error {
			collected = append(collected, item)
			return nil
		})
	return collected, encodeCursor(newCursor), err
}

// FetchStream is the production sync path. It emits each book as it is
// downloaded and checkpoints at the end, so a large catalog stays
// memory-bounded (one file at a time, not every book at once) and a sync that
// times out resumes instead of restarting.
func (c *Connector) FetchStream(
	ctx context.Context, config *types.DataSourceConfig,
	cursor *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	prev := decodeCursor(ctx, cursor)

	// A stream that fails mid-run still has a usable cursor for the feeds it
	// completed, so persist it rather than discarding progress.
	newCursor, err := c.walk(ctx, config, config.ResourceIDs, prev, cursor != nil,
		func(item types.FetchedItem) error {
			return h.Emit(ctx, item)
		})
	if newCursor != nil {
		if cpErr := h.Checkpoint(ctx, encodeCursor(newCursor)); cpErr != nil {
			logger.Warnf(ctx, "[OPDS] checkpoint failed: %v", cpErr)
		}
	}
	return encodeCursor(newCursor), err
}

// decodeCursor unmarshals a stored sync cursor, tolerating a missing or
// unreadable one by starting from an empty state.
func decodeCursor(ctx context.Context, cursor *types.SyncCursor) *opdsCursor {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return nil
	}
	b, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		logger.Warnf(ctx, "[OPDS] marshal connector cursor: %v", err)
		return nil
	}
	var prev opdsCursor
	if err := json.Unmarshal(b, &prev); err != nil {
		logger.Warnf(ctx, "[OPDS] unmarshal connector cursor: %v", err)
		return nil
	}
	return &prev
}

// encodeCursor converts the internal cursor into the generic SyncCursor form.
func encodeCursor(c *opdsCursor) *types.SyncCursor {
	if c == nil {
		return nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return &types.SyncCursor{
		LastSyncTime:    c.LastSyncTime,
		ConnectorCursor: m,
	}
}

// emitFunc receives one fully-downloaded item.
type emitFunc func(item types.FetchedItem) error

// emitError marks a failure raised by the caller's emit callback. It lets walk
// distinguish "the knowledge ingest failed" (which must abort the sync) from
// "the catalog could not be read" (which degrades to a partial failure), rather
// than reporting a broken ingest as an unreachable catalog.
type emitError struct{ err error }

func (e *emitError) Error() string { return e.err.Error() }
func (e *emitError) Unwrap() error { return e.err }

// emitErrOf returns the caller's original emit error when err came from the
// emit callback, and nil otherwise. It lets walk tell "the knowledge ingest
// failed" (which must abort the sync) apart from "the catalog could not be
// read" (which degrades to a partial failure), rather than reporting a broken
// ingest as an unreachable catalog.
func emitErrOf(err error) error {
	var ee *emitError
	if errors.As(err, &ee) {
		return ee.err
	}
	return nil
}

// walkState carries the mutable state of one traversal.
//
// Feed-level failures and item-level failures are tracked separately on
// purpose: only a feed that could not be read at all should count toward "the
// whole sync failed". A catalog that reads fine but whose books all fail to
// download is a partial failure, not a dead source.
type walkState struct {
	cursor     *opdsCursor
	feedErrors []string
	itemErrors []string
	visited    map[string]struct{}

	// rootsTotal / rootsFailed count only the top-level selections, not the
	// sub-feeds reached while traversing them. "Every feed failed" must be
	// judged against what the user selected: a root whose sections partly
	// succeeded is a partial success even if one section was unreachable.
	rootsTotal  int
	rootsFailed int
}

// issues returns every problem recorded so far, for reporting.
func (s *walkState) issues() []string {
	return append(append([]string{}, s.feedErrors...), s.itemErrors...)
}

// walk is the shared traversal behind FetchAll / FetchIncremental / FetchStream.
//
// It visits each selected feed; a feed that turns out to be a navigation feed
// has its direct sub-feeds explored one level down (maxNavigationDepth). Every
// acquisition entry is downloaded and emitted unless the incremental layer
// proves it unchanged.
//
// It returns the updated cursor plus an error: a *datasource.PartialFetchError
// when anything failed but the sync still made progress, a hard error only when
// every selected feed was unreadable.
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *opdsCursor,
	incremental bool,
	emit emitFunc,
) (*opdsCursor, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}

	feedURLs := resourceIDs
	if len(feedURLs) == 0 {
		feedURLs = cfg.catalogURLList()
	}

	cli := newClient(cfg)
	maxBookSize := utils.GetMaxFileSize()

	st := &walkState{
		cursor: &opdsCursor{
			LastSyncTime: time.Now().UTC(),
			Signals:      make(map[string]map[string]string),
			Hashes:       make(map[string]map[string]string),
		},
		visited: make(map[string]struct{}),
	}

	for _, feedURL := range feedURLs {
		if _, seen := st.visited[feedURL]; seen {
			continue
		}
		st.visited[feedURL] = struct{}{}
		st.rootsTotal++

		if err := c.walkFeed(ctx, cli, feedURL, 0, prev, incremental, maxBookSize, st, emit); err != nil {
			// An ingest failure is not a catalog failure: abort the sync and
			// surface the real error rather than mislabelling it below.
			if inner := emitErrOf(err); inner != nil {
				return st.cursor, inner
			}
			logger.Warnf(ctx, "[OPDS] sync: %s failed: %v", feedURL, err)
			st.feedErrors = append(st.feedErrors, fmt.Sprintf("%s: %v", feedURL, err))
			st.rootsFailed++
			copyFeedCursor(st.cursor, prev, feedURL)
		}
	}

	// Every selected root was unreadable: the source itself is broken, so this
	// is a hard failure rather than a partial one.
	if st.rootsFailed > 0 && st.rootsFailed >= st.rootsTotal {
		return st.cursor, fmt.Errorf("all catalogs failed: %s", strings.Join(st.feedErrors, "; "))
	}
	if issues := st.issues(); len(issues) > 0 {
		return st.cursor, &datasource.PartialFetchError{Details: issues}
	}
	return st.cursor, nil
}

// walkFeed fetches one feed, emits its acquisition entries, and — when it is a
// navigation feed still within the depth budget — recurses into its direct
// sub-feeds. A feed already visited in this run is skipped so a catalog whose
// sections link to each other cannot loop.
func (c *Connector) walkFeed(
	ctx context.Context,
	cli *client,
	feedURL string,
	depth int,
	prev *opdsCursor,
	incremental bool,
	maxBookSize int64,
	st *walkState,
	emit emitFunc,
) error {
	var feed *opdsFeed
	if err := cli.walkPages(ctx, feedURL, func(page *opdsFeed, base *url.URL) error {
		feed = page
		return c.emitEntries(ctx, cli, feedURL, base, page, prev, incremental,
			maxBookSize, st, emit)
	}); err != nil {
		return err
	}
	if feed == nil {
		return nil
	}

	// A navigation feed has no books of its own; explore its direct sub-feeds
	// one level down so selecting a root catalog still syncs its sections.
	if feedKindOf(feed) == kindAcquisition || depth >= maxNavigationDepth {
		return nil
	}

	base, _ := url.Parse(feedURL)
	for i := range feed.Entries {
		nav := navigationLink(&feed.Entries[i])
		if nav == nil {
			continue
		}
		child, err := resolveHref(base, nav.Href)
		if err != nil {
			continue
		}
		if _, seen := st.visited[child]; seen {
			continue
		}
		st.visited[child] = struct{}{}

		if err := c.walkFeed(ctx, cli, child, depth+1, prev, incremental,
			maxBookSize, st, emit); err != nil {
			if inner := emitErrOf(err); inner != nil {
				return &emitError{err: inner}
			}
			logger.Warnf(ctx, "[OPDS] sync: sub-feed %s failed: %v", child, err)
			st.feedErrors = append(st.feedErrors, fmt.Sprintf("%s: %v", child, err))
			copyFeedCursor(st.cursor, prev, child)
		}
	}
	return nil
}

// emitEntries processes one page of a feed: for each entry with an acquisition
// link, it decides whether a download is needed and, if so, downloads and
// emits the book.
func (c *Connector) emitEntries(
	ctx context.Context,
	cli *client,
	feedURL string,
	base *url.URL,
	feed *opdsFeed,
	prev *opdsCursor,
	incremental bool,
	maxBookSize int64,
	st *walkState,
	emit emitFunc,
) error {
	if len(feed.Entries) == 0 {
		return nil
	}
	if st.cursor.Signals[feedURL] == nil {
		st.cursor.Signals[feedURL] = make(map[string]string)
	}
	if st.cursor.Hashes[feedURL] == nil {
		st.cursor.Hashes[feedURL] = make(map[string]string)
	}

	var prevSignals, prevHashes map[string]string
	if incremental && prev != nil {
		prevSignals = prev.Signals[feedURL]
		prevHashes = prev.Hashes[feedURL]
	}

	var kept, skipped int
	for i := range feed.Entries {
		entry := &feed.Entries[i]

		id := entryID(entry)
		if id == "" {
			continue
		}
		acq := acquisitionLink(entry)
		if acq == nil {
			continue // a navigation entry, or a book with nothing we can fetch
		}

		ext := extensionForLink(acq)
		if !isSupportedAcquisitionExtension(ext) {
			// A format the pipeline cannot parse (mobi, azw3, cbz, …). Skip it
			// rather than failing the sync.
			logger.Infof(ctx, "[OPDS] skipping %q: unsupported acquisition type %q (%s)",
				entry.Title, acq.Type, ext)
			continue
		}

		signal := entrySignalFingerprint(entry)
		if incremental && prevSignals != nil && prevSignals[id] == signal {
			// Nothing in the feed changed: carry the prior state forward and
			// skip the download entirely.
			st.cursor.Signals[feedURL][id] = signal
			if h, ok := prevHashes[id]; ok {
				st.cursor.Hashes[feedURL][id] = h
			}
			skipped++
			continue
		}

		bookURL, err := resolveHref(base, acq.Href)
		if err != nil {
			logger.Warnf(ctx, "[OPDS] entry %q: %v", entry.Title, err)
			continue
		}

		content, err := cli.fetchBook(ctx, bookURL, maxBookSize)
		if err != nil {
			// A single unreachable or oversized book must not abort the sync.
			logger.Warnf(ctx, "[OPDS] download %q failed: %v", entry.Title, err)
			st.itemErrors = append(st.itemErrors,
				fmt.Sprintf("%s: download failed: %v", firstNonEmpty(entry.Title, id), err))
			// Keep the previous fingerprint so a later run retries the download.
			if prevSignals != nil {
				if s, ok := prevSignals[id]; ok {
					st.cursor.Signals[feedURL][id] = s
				}
				if h, ok := prevHashes[id]; ok {
					st.cursor.Hashes[feedURL][id] = h
				}
			}
			continue
		}

		hash := contentFingerprint(content)
		st.cursor.Signals[feedURL][id] = signal
		st.cursor.Hashes[feedURL][id] = hash

		if incremental && prevHashes != nil && prevHashes[id] == hash {
			// Metadata moved but the file bytes did not: nothing to re-ingest.
			skipped++
			continue
		}

		kept++
		if err := emit(c.item(feedURL, entry, bookURL, ext, acq.Type, content)); err != nil {
			return &emitError{err: err}
		}
	}

	logger.Infof(ctx, "[OPDS] feed %s: entries=%d emitted=%d skipped=%d",
		feedURL, len(feed.Entries), kept, skipped)
	return nil
}

// item assembles the FetchedItem for one book. Content bytes are set so the
// generic ingestion path routes it through CreateKnowledgeFromFile and the
// normal document parse pipeline.
func (c *Connector) item(
	feedURL string, entry *opdsEntry, bookURL, ext, contentType string, content []byte,
) types.FetchedItem {
	title := firstNonEmpty(entry.Title, "untitled")

	updatedAt := time.Now().UTC()
	for _, raw := range []string{entry.Updated, entry.Published} {
		if raw == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			updatedAt = t.UTC()
			break
		}
	}

	return types.FetchedItem{
		ExternalID:       itemExternalID(feedURL, entryID(entry)),
		Title:            title,
		Content:          content,
		ContentType:      contentType,
		FileName:         sanitizeFileName(title) + "." + ext,
		URL:              bookURL,
		UpdatedAt:        updatedAt,
		SourceResourceID: feedURL,
		Metadata: map[string]string{
			"channel":         types.ChannelOPDS,
			"catalog_url":     feedURL,
			"entry_id":        entryID(entry),
			"acquisition_url": bookURL,
			"author":          entryAuthor(entry),
		},
	}
}
