package rss

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mmcdole/gofeed"
)

// Compile-time proof that *Connector satisfies the datasource.Connector interface.
var _ datasource.Connector = (*Connector)(nil)

// Connector implements datasource.Connector for RSS/Atom/JSON feeds.
type Connector struct{}

// NewConnector creates a new RSS connector.
func NewConnector() *Connector { return &Connector{} }

// Type returns the connector type identifier.
func (c *Connector) Type() string { return types.ConnectorTypeRSS }

// Validate verifies that every configured feed URL is reachable and parses as
// a valid feed.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseConfig(config)
	if err != nil {
		return err
	}
	cli := newClient(cfg.parseHeaders())
	parser := gofeed.NewParser()

	for _, feedURL := range cfg.feedURLList() {
		data, err := cli.fetchFeed(ctx, feedURL)
		if err != nil {
			return fmt.Errorf("fetch feed %s: %w", feedURL, err)
		}
		if _, err := parser.Parse(bytes.NewReader(data)); err != nil {
			return fmt.Errorf("parse feed %s: %w", feedURL, err)
		}
	}
	return nil
}

// ResolveResourceAncestors has nothing to do: feeds are a flat list with no
// nesting, so a selection has no ancestors to reveal.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	return []string{}, nil
}

// ListResources returns one resource per configured feed URL. The feed is
// fetched so the resource can carry its real title; a feed that fails to fetch
// still appears (named by URL) with an error note, so the user can deselect it
// instead of the whole listing failing.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	// Feeds are flat: a lazy-load request for a specific parent has nothing extra.
	if parentID != "" {
		return []types.Resource{}, nil
	}

	cfg, err := parseConfig(config)
	if err != nil {
		return nil, err
	}
	cli := newClient(cfg.parseHeaders())
	parser := gofeed.NewParser()

	feedURLs := cfg.feedURLList()
	out := make([]types.Resource, 0, len(feedURLs))
	for _, feedURL := range feedURLs {
		res := types.Resource{
			ExternalID: feedURL,
			Type:       "feed",
			Name:       feedURL,
			URL:        feedURL,
		}
		data, err := cli.fetchFeed(ctx, feedURL)
		if err != nil {
			logger.Warnf(ctx, "[RSS] list: fetch %s failed: %v", feedURL, err)
			res.Description = "fetch failed: " + err.Error()
			out = append(out, res)
			continue
		}
		feed, err := parser.Parse(bytes.NewReader(data))
		if err != nil {
			logger.Warnf(ctx, "[RSS] list: parse %s failed: %v", feedURL, err)
			res.Description = "parse failed: " + err.Error()
			out = append(out, res)
			continue
		}
		if title := strings.TrimSpace(feed.Title); title != "" {
			res.Name = title
		}
		res.Description = strings.TrimSpace(feed.Description)
		if feed.Link != "" {
			res.URL = feed.Link
		}
		if feed.UpdatedParsed != nil {
			res.ModifiedAt = *feed.UpdatedParsed
		}
		res.Metadata = map[string]interface{}{"item_count": len(feed.Items)}
		out = append(out, res)
	}
	return out, nil
}

// FetchAll performs a full sync of the specified feeds (or all configured feeds
// when resourceIDs is empty).
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.walk(ctx, config, resourceIDs, nil, false)
	return items, err
}

// FetchIncremental returns only items whose content fingerprint changed since
// the prior cursor. Deletions are intentionally not emitted (feeds drop old
// items as a matter of course).
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	var prev *rssCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		var p rssCursor
		b, err := json.Marshal(cursor.ConnectorCursor)
		if err != nil {
			logger.Warnf(ctx, "[RSS] marshal connector cursor: %v", err)
		} else if err := json.Unmarshal(b, &p); err != nil {
			logger.Warnf(ctx, "[RSS] unmarshal connector cursor: %v", err)
		} else {
			prev = &p
		}
	}

	items, newCursor, err := c.walk(ctx, config, config.ResourceIDs, prev, true)
	if err != nil && newCursor == nil {
		return nil, nil, err
	}

	cursorMap := make(map[string]interface{})
	if newCursor != nil {
		b, marshalErr := json.Marshal(newCursor)
		if marshalErr != nil {
			logger.Warnf(ctx, "[RSS] marshal new cursor: %v", marshalErr)
		} else if unmarshalErr := json.Unmarshal(b, &cursorMap); unmarshalErr != nil {
			logger.Warnf(ctx, "[RSS] unmarshal new cursor to map: %v", unmarshalErr)
		}
	}

	var syncCursor *types.SyncCursor
	if newCursor != nil {
		syncCursor = &types.SyncCursor{
			LastSyncTime:    newCursor.LastSyncTime,
			ConnectorCursor: cursorMap,
		}
	}

	return items, syncCursor, err
}

// walk is the shared implementation for FetchAll / FetchIncremental.
//
// When incremental is true, items whose feed signal and content fingerprint are
// both unchanged are omitted from the result (ingest skipped) without fetching
// article pages. The returned cursor always reflects the best-known item state
// so a later sync can detect changes.
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *rssCursor,
	incremental bool,
) ([]types.FetchedItem, *rssCursor, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}

	// Default to all configured feeds when no explicit selection was made.
	feedURLs := resourceIDs
	if len(feedURLs) == 0 {
		feedURLs = cfg.feedURLList()
	}

	cli := newClient(cfg.parseHeaders())
	parser := gofeed.NewParser()

	newCursor := &rssCursor{
		LastSyncTime: time.Now().UTC(),
		FeedItems:    make(map[string]map[string]string),
		FeedSignals:  make(map[string]map[string]string),
	}
	var out []types.FetchedItem
	var feedErrors []string

	for _, feedURL := range feedURLs {
		data, err := cli.fetchFeed(ctx, feedURL)
		if err != nil {
			logger.Warnf(ctx, "[RSS] sync: fetch %s failed: %v", feedURL, err)
			feedErrors = append(feedErrors, fmt.Sprintf("%s: %v", feedURL, err))
			copyFeedCursor(newCursor, prev, feedURL)
			continue
		}
		feed, err := parser.Parse(bytes.NewReader(data))
		if err != nil {
			logger.Warnf(ctx, "[RSS] sync: parse %s failed: %v", feedURL, err)
			feedErrors = append(feedErrors, fmt.Sprintf("%s: %v", feedURL, err))
			copyFeedCursor(newCursor, prev, feedURL)
			continue
		}

		newCursor.FeedItems[feedURL] = make(map[string]string)
		newCursor.FeedSignals[feedURL] = make(map[string]string)
		var prevItems map[string]string
		var prevSignals map[string]string
		if incremental && prev != nil {
			prevItems = prev.FeedItems[feedURL]
			if prev.FeedSignals != nil {
				prevSignals = prev.FeedSignals[feedURL]
			}
		}

		var kept, skipped int
		for _, item := range feed.Items {
			if item == nil {
				continue
			}
			itemID := firstNonEmpty(item.GUID, item.Link, item.Title)
			if itemID == "" {
				continue
			}

			feedContent := firstNonEmpty(item.Content, item.Description)
			feedSig := feedSignalFingerprint(item, feedContent)

			if incremental && prevItems != nil {
				prevFP := prevItems[itemID]
				prevSig := ""
				if prevSignals != nil {
					prevSig = prevSignals[itemID]
				}
				if prevFP != "" && feedSig == prevSig {
					newCursor.FeedItems[feedURL][itemID] = prevFP
					newCursor.FeedSignals[feedURL][itemID] = feedSig
					skipped++
					continue
				}
			}

			resolved := c.resolveItem(ctx, cli, feed, item, feedURL, itemID, feedContent)
			newCursor.FeedItems[feedURL][itemID] = resolved.fingerprint
			newCursor.FeedSignals[feedURL][itemID] = feedSig

			if incremental && prevItems != nil && prevItems[itemID] == resolved.fingerprint {
				skipped++
				continue
			}
			kept++
			out = append(out, resolved.item)
		}

		logger.Infof(ctx, "[RSS] feed %s: items=%d fetched=%d skipped=%d",
			feedURL, len(feed.Items), kept, skipped)
	}

	if len(feedErrors) > 0 {
		if len(out) == 0 && len(feedErrors) == len(feedURLs) {
			return nil, newCursor, fmt.Errorf("all feeds failed: %s", strings.Join(feedErrors, "; "))
		}
		return out, newCursor, &datasource.PartialFetchError{Details: feedErrors}
	}

	return out, newCursor, nil
}

type resolvedFeedItem struct {
	item        types.FetchedItem
	fingerprint string
}

// resolveItem assembles a FetchedItem for a single feed entry, resolving the
// best available content (full-text article > feed content) and converting it
// to Markdown.
func (c *Connector) resolveItem(
	ctx context.Context,
	cli *client,
	feed *gofeed.Feed,
	item *gofeed.Item,
	feedURL, itemID, feedContent string,
) resolvedFeedItem {
	title := firstNonEmpty(item.Title, "untitled")

	// Prefer full article text; fall back to feed-provided content on failure.
	contentHTML := feedContent
	if strings.TrimSpace(item.Link) != "" {
		if articleHTML, articleTitle, err := cli.extractArticle(ctx, item.Link); err == nil {
			contentHTML = articleHTML
			if item.Title == "" && articleTitle != "" {
				title = articleTitle
			}
		} else {
			logger.Warnf(ctx, "[RSS] full-text fetch failed for %s (using feed content): %v", item.Link, err)
		}
	}

	content := htmlToMarkdown(contentHTML)

	updatedAt := time.Now().UTC()
	switch {
	case item.UpdatedParsed != nil && !item.UpdatedParsed.IsZero():
		updatedAt = *item.UpdatedParsed
	case item.PublishedParsed != nil && !item.PublishedParsed.IsZero():
		updatedAt = *item.PublishedParsed
	}

	author := ""
	if item.Author != nil {
		author = item.Author.Name
	}

	return resolvedFeedItem{
		fingerprint: contentFingerprint(content),
		item: types.FetchedItem{
			ExternalID:       itemExternalID(feedURL, itemID),
			Title:            title,
			Content:          []byte(content),
			ContentType:      "text/markdown",
			FileName:         sanitizeFileName(title) + ".md",
			URL:              item.Link,
			UpdatedAt:        updatedAt,
			SourceResourceID: feedURL,
			Metadata: map[string]string{
				"channel":    types.ChannelRSS,
				"feed_url":   feedURL,
				"feed_title": feed.Title,
				"guid":       item.GUID,
				"link":       item.Link,
				"author":     author,
			},
		},
	}
}

// htmlToMarkdown converts HTML to Markdown, returning the trimmed original on
// conversion failure so we never silently drop content.
func htmlToMarkdown(html string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}
	md, err := htmltomd.ConvertString(html)
	if err != nil || strings.TrimSpace(md) == "" {
		return strings.TrimSpace(html)
	}
	return strings.TrimSpace(md)
}
