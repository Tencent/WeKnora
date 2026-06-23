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

// FetchIncremental returns only items whose fingerprint changed since the prior
// cursor. Deletions are intentionally not emitted (feeds drop old items as a
// matter of course).
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	var prev *rssCursor
	if cursor != nil && cursor.ConnectorCursor != nil {
		var p rssCursor
		b, _ := json.Marshal(cursor.ConnectorCursor)
		_ = json.Unmarshal(b, &p)
		prev = &p
	}

	items, newCursor, err := c.walk(ctx, config, config.ResourceIDs, prev, true)
	if err != nil {
		return nil, nil, err
	}

	cursorMap := make(map[string]interface{})
	b, _ := json.Marshal(newCursor)
	_ = json.Unmarshal(b, &cursorMap)

	return items, &types.SyncCursor{
		LastSyncTime:    newCursor.LastSyncTime,
		ConnectorCursor: cursorMap,
	}, nil
}

// walk is the shared implementation for FetchAll / FetchIncremental.
//
// When incremental is true, items whose fingerprint matches the prior cursor
// are skipped (no article re-fetch). The returned cursor always reflects the
// full current item set so a later sync can detect changes.
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
	}
	var out []types.FetchedItem

	for _, feedURL := range feedURLs {
		data, err := cli.fetchFeed(ctx, feedURL)
		if err != nil {
			return nil, nil, fmt.Errorf("fetch feed %s: %w", feedURL, err)
		}
		feed, err := parser.Parse(bytes.NewReader(data))
		if err != nil {
			return nil, nil, fmt.Errorf("parse feed %s: %w", feedURL, err)
		}

		newCursor.FeedItems[feedURL] = make(map[string]string)
		var prevItems map[string]string
		if incremental && prev != nil {
			prevItems = prev.FeedItems[feedURL]
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
			fp := itemFingerprint(item.UpdatedParsed, item.PublishedParsed, feedContent)
			newCursor.FeedItems[feedURL][itemID] = fp

			if incremental && prevItems != nil && prevItems[itemID] == fp {
				skipped++
				continue
			}
			kept++

			out = append(out, c.buildItem(ctx, cli, feed, item, feedURL, itemID, feedContent))
		}

		logger.Infof(ctx, "[RSS] feed %s: items=%d fetched=%d skipped=%d",
			feedURL, len(feed.Items), kept, skipped)
	}

	if !incremental {
		return out, newCursor, nil
	}
	return out, newCursor, nil
}

// buildItem assembles a FetchedItem for a single feed entry, resolving the
// best available content (full-text article > feed content) and converting it
// to Markdown.
func (c *Connector) buildItem(
	ctx context.Context,
	cli *client,
	feed *gofeed.Feed,
	item *gofeed.Item,
	feedURL, itemID, feedContent string,
) types.FetchedItem {
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

	return types.FetchedItem{
		ExternalID:       itemID,
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
