// Package rss implements the RSS/Atom data source connector for WeKnora.
//
// It syncs articles from one or more RSS/Atom/JSON feeds into a WeKnora
// knowledge base. Each configured feed URL is treated as a selectable
// resource; each feed item becomes a knowledge entry whose body is the
// article's full text rendered as Markdown.
//
// Capabilities:
//   - Private feeds: optional custom HTTP headers (e.g. "Authorization: Bearer …")
//     are attached only to feed fetches, never to third-party article pages.
//   - Full text: when an item exposes a link, the article page is fetched and
//     run through a readability extractor; the cleaned HTML is converted to
//     Markdown. Feed-provided content (content:encoded / description) is used
//     as a fallback.
//   - Incremental: a per-item fingerprint hashes the final Markdown body that
//     would be ingested, so feed-only metadata changes and full-article edits
//     are both detected. Deletions are NOT synced — feeds routinely drop old
//     items, which would otherwise look like deletions.
//
// All outbound requests go through the SSRF-safe HTTP client so a malicious
// feed cannot redirect WeKnora to internal services.
package rss

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// Config holds RSS-specific configuration.
//
// FeedURLs are stored in DataSourceConfig.Settings (non-secret, editable in
// the UI without replacing credentials). AuthHeaders live in Credentials
// because they may carry secrets that must be encrypted at rest. Credentials
// may still carry feed_urls for backward compatibility with older rows.
type Config struct {
	// FeedURLs is a newline- or comma-separated list of feed URLs.
	FeedURLs string `json:"feed_urls"`

	// AuthHeaders is an optional newline-separated list of custom request
	// headers in "Name: Value" form, applied only to feed fetches.
	AuthHeaders string `json:"auth_headers,omitempty"`
}

// parseConfig extracts and validates RSS configuration.
func parseConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}
	credBytes, err := json.Marshal(config.Credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(credBytes, &cfg); err != nil {
		return nil, fmt.Errorf("parse rss credentials: %w", err)
	}
	if urls := feedURLsFromSettings(config.Settings); urls != "" {
		cfg.FeedURLs = urls
	}
	if len(cfg.feedURLList()) == 0 {
		return nil, fmt.Errorf("%w: feed_urls is required", datasource.ErrInvalidCredentials)
	}
	return &cfg, nil
}

func feedURLsFromSettings(settings map[string]interface{}) string {
	if len(settings) == 0 {
		return ""
	}
	raw, ok := settings["feed_urls"]
	if !ok {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// feedURLList splits FeedURLs on newlines and commas, trims, dedupes (order
// preserved), and drops blanks.
func (c *Config) feedURLList() []string {
	if c == nil {
		return nil
	}
	raw := strings.FieldsFunc(c.FeedURLs, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	})
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, u := range raw {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

// parseHeaders turns the newline-separated "Name: Value" AuthHeaders blob into
// a map. Lines without a colon, or with an empty name, are skipped.
func (c *Config) parseHeaders() map[string]string {
	if c == nil || strings.TrimSpace(c.AuthHeaders) == "" {
		return nil
	}
	headers := make(map[string]string)
	for _, line := range strings.Split(c.AuthHeaders, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		if name == "" {
			continue
		}
		headers[name] = value
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// rssCursor stores incremental sync state.
//
// FeedItems maps feedURL → itemID → fingerprint, where fingerprint is a
// "h:<sha256-prefix>" hash of the final Markdown body that would be ingested.
type rssCursor struct {
	LastSyncTime time.Time                    `json:"last_sync_time"`
	FeedItems    map[string]map[string]string `json:"feed_items,omitempty"`
}

// contentFingerprint hashes ingested Markdown for incremental change detection.
func contentFingerprint(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return "h:" + hex.EncodeToString(sum[:])[:16]
}

// itemExternalID scopes an item ID to its feed so GUIDs cannot collide across feeds.
func itemExternalID(feedURL, itemID string) string {
	return feedURL + ":" + itemID
}

// firstNonEmpty returns the first non-empty trimmed string among the args.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// sanitizeFileName removes characters invalid in filenames and truncates to a
// safe length at a UTF-8 rune boundary (mirrors the Yuque connector).
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
		"\n", " ", "\r", " ", "\t", " ",
	)
	result := strings.TrimSpace(replacer.Replace(name))
	if result == "" {
		return "untitled"
	}
	const maxBytes = 200
	if len(result) > maxBytes {
		result = result[:maxBytes]
		for len(result) > 0 {
			r, size := utf8.DecodeLastRuneInString(result)
			if r != utf8.RuneError || size != 1 {
				break
			}
			result = result[:len(result)-1]
		}
	}
	return result
}
