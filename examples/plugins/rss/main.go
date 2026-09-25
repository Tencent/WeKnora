// Command rss is WeKnora's RSS/Atom/JSON feed connector written as an
// external plugin with the Go SDK. It behaves like the builtin rss connector
// (a test syncs the same feeds through both) and shows how a code plugin is
// built: it imports only the SDK and ordinary libraries, never WeKnora's
// internals.
//
// Build a package with ./package.sh; install it under System administration
// → Plugin management.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Version must match plugin.yaml.
const Version = "1.0.0"

var errMissingFeeds = pluginapi.InvalidConfig(
	"feed_urls is required",
	map[string]string{"settings.feed_urls": "required"},
)

func main() {
	p := pluginsdk.New(pluginsdk.Info{ID: "weknora-examples.rss", Version: Version})
	p.Connector("rss", &connector{logf: p.Logger().Warn})
	if err := p.Serve(); err != nil {
		log.Fatal(err)
	}
}

type connector struct {
	logf func(msg string, args ...any)
}

func (c *connector) warn(msg string, args ...any) {
	if c.logf != nil {
		c.logf(msg, args...)
	}
}

// Validate fetches and parses every configured feed.
func (c *connector) Validate(ctx context.Context, _ *pluginsdk.Call, in pluginsdk.ConnectorConfig) error {
	cfg, err := parseConfig(in.Settings, in.Credentials)
	if err != nil {
		return err
	}
	f := newFetcher(cfg.authHeaders)
	for _, u := range cfg.feedURLs {
		if _, err := f.feed(ctx, u); err != nil {
			return pluginapi.InvalidConfig(fmt.Sprintf("feed %s: %v", u, err),
				map[string]string{"settings.feed_urls": err.Error()})
		}
	}
	return nil
}

// ListResources lists one resource per feed. A feed that fails still
// appears, named by its URL, so the user can deselect it.
func (c *connector) ListResources(
	ctx context.Context, _ *pluginsdk.Call, in pluginsdk.ConnectorConfig, parentID string,
) ([]pluginapi.Resource, error) {
	if parentID != "" {
		return nil, nil
	}
	cfg, err := parseConfig(in.Settings, in.Credentials)
	if err != nil {
		return nil, err
	}
	f := newFetcher(cfg.authHeaders)
	out := make([]pluginapi.Resource, 0, len(cfg.feedURLs))
	for _, u := range cfg.feedURLs {
		res := pluginapi.Resource{ExternalID: u, Type: "feed", Name: u, URL: u}
		feed, err := f.feed(ctx, u)
		if err != nil {
			res.Description = "fetch failed: " + err.Error()
			out = append(out, res)
			continue
		}
		if t := strings.TrimSpace(feed.Title); t != "" {
			res.Name = t
		}
		res.Description = strings.TrimSpace(feed.Description)
		if feed.Link != "" {
			res.URL = feed.Link
		}
		res.ModifiedAt = feed.UpdatedParsed
		res.Metadata = map[string]any{"item_count": len(feed.Items)}
		out = append(out, res)
	}
	return out, nil
}

// Fetch syncs the selected feeds (all configured ones when none are
// selected). Incremental runs skip entries whose feed signal and content are
// unchanged; deletions are never emitted, since feeds drop old items as a
// matter of course.
func (c *connector) Fetch(
	ctx context.Context,
	_ *pluginsdk.Call,
	in pluginsdk.ConnectorConfig,
	req pluginapi.FetchInput,
	s *pluginsdk.Stream,
) (*pluginapi.Cursor, error) {
	cfg, err := parseConfig(in.Settings, in.Credentials)
	if err != nil {
		return nil, err
	}
	feedURLs := req.ResourceIDs
	if len(feedURLs) == 0 {
		feedURLs = cfg.feedURLs
	}
	var prev *cursorState
	incremental := req.Mode == pluginapi.FetchIncremental && req.Cursor != nil
	if incremental {
		prev = cursorFromState(req.Cursor.State)
	}
	f := newFetcher(cfg.authHeaders)
	next := newCursorState()
	now := time.Now().UTC()
	var failures []string

	for _, feedURL := range feedURLs {
		feed, err := f.feed(ctx, feedURL)
		if err != nil {
			c.warn("feed failed", "url", feedURL, "err", err)
			failures = append(failures, fmt.Sprintf("%s: %v", feedURL, err))
			next.copyFeed(prev, feedURL)
			continue
		}
		next.FeedItems[feedURL] = map[string]string{}
		next.FeedSignals[feedURL] = map[string]string{}
		var prevItems, prevSignals map[string]string
		if prev != nil {
			prevItems, prevSignals = prev.FeedItems[feedURL], prev.FeedSignals[feedURL]
		}
		for _, item := range feed.Items {
			if item == nil {
				continue
			}
			itemID := firstNonEmpty(item.GUID, item.Link, item.Title)
			if itemID == "" {
				continue
			}
			feedContent := firstNonEmpty(item.Content, item.Description)
			sig := feedSignal(item, feedContent)
			if incremental && prevItems[itemID] != "" && prevSignals[itemID] == sig {
				next.FeedItems[feedURL][itemID] = prevItems[itemID]
				next.FeedSignals[feedURL][itemID] = sig
				continue
			}
			fetched, fp := c.resolve(ctx, f, feed, item, feedURL, itemID, feedContent)
			next.FeedItems[feedURL][itemID] = fp
			next.FeedSignals[feedURL][itemID] = sig
			if incremental && prevItems[itemID] == fp {
				continue
			}
			if err := s.Item(fetched); err != nil {
				return nil, err
			}
		}
		// Each finished feed is a safe place to resume from.
		if err := s.Checkpoint(pluginapi.Cursor{LastSyncTime: &now, State: next.toState()}); err != nil {
			return nil, err
		}
	}
	if len(failures) > 0 && len(failures) == len(feedURLs) {
		return nil, pluginapi.Errorf(pluginapi.CodeUnavailable, "all feeds failed: %s", strings.Join(failures, "; "))
	}
	if len(failures) > 0 {
		_ = s.Log("warn", "some feeds failed: "+strings.Join(failures, "; "))
	}
	return &pluginapi.Cursor{LastSyncTime: &now, State: next.toState()}, nil
}

// resolve builds one item: the article's full text when it can be fetched,
// the feed's own content otherwise, as Markdown.
func (c *connector) resolve(
	ctx context.Context, f *fetcher, feed *gofeed.Feed, item *gofeed.Item, feedURL, itemID, feedContent string,
) (pluginapi.FetchedItem, string) {
	title := firstNonEmpty(item.Title, "untitled")
	contentHTML := feedContent
	if strings.TrimSpace(item.Link) != "" {
		html, articleTitle, err := f.article(ctx, item.Link)
		if err == nil {
			contentHTML = html
			if item.Title == "" && articleTitle != "" {
				title = articleTitle
			}
		} else if !errors.Is(err, context.Canceled) {
			c.warn("full-text fetch failed, using feed content", "url", item.Link, "err", err)
		}
	}
	content := htmlToMarkdown(contentHTML)
	updated := time.Now().UTC()
	switch {
	case item.UpdatedParsed != nil && !item.UpdatedParsed.IsZero():
		updated = *item.UpdatedParsed
	case item.PublishedParsed != nil && !item.PublishedParsed.IsZero():
		updated = *item.PublishedParsed
	}
	author := ""
	if item.Author != nil {
		author = item.Author.Name
	}
	return pluginapi.FetchedItem{
		ExternalID:       feedURL + ":" + itemID,
		Title:            title,
		Content:          []byte(content),
		ContentType:      "text/markdown",
		FileName:         fileName(title),
		URL:              item.Link,
		UpdatedAt:        &updated,
		SourceResourceID: feedURL,
		Metadata: map[string]string{
			"channel": "rss", "feed_url": feedURL, "feed_title": feed.Title,
			"guid": item.GUID, "link": item.Link, "author": author,
		},
	}, contentFingerprint(content)
}
