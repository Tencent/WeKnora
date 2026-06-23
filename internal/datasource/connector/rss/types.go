// Package rss implements the RSS/Atom data source connector for WeKnora.
//
// It syncs articles from one or more RSS/Atom/JSON feeds into a WeKnora
// knowledge base. Each configured feed URL is treated as a selectable
// resource; each feed item becomes a knowledge entry whose body is the
// article's full text rendered as Markdown.
//
// Capabilities:
//   - Private feeds: optional custom HTTP headers (e.g. "Authorization: Bearer …")
//     are attached to every request, both to the feed and to article pages.
//   - Full text: when an item exposes a link, the article page is fetched and
//     run through a readability extractor; the cleaned HTML is converted to
//     Markdown. Feed-provided content (content:encoded / description) is used
//     as a fallback.
//   - Incremental: a per-feed fingerprint (item updated/published time, or a
//     content hash when no timestamp is present) detects changed items so only
//     new/updated articles are re-fetched. Deletions are NOT synced — feeds
//     routinely drop old items, which would otherwise look like deletions.
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

// Config holds RSS-specific configuration parsed from DataSourceConfig.Credentials.
//
// Both fields live under Credentials (rather than Settings) because the
// existing data-source editor UI only renders credential inputs, and because
// AuthHeaders may carry secrets that must be encrypted at rest. FeedURLs are
// not secret but ride along for the same single-input flow.
type Config struct {
	// FeedURLs is a newline- or comma-separated list of feed URLs.
	FeedURLs string `json:"feed_urls"`

	// AuthHeaders is an optional newline-separated list of custom request
	// headers in "Name: Value" form, applied to every outbound request.
	AuthHeaders string `json:"auth_headers,omitempty"`
}

// parseConfig extracts and validates RSS configuration from the credentials map.
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
	if len(cfg.feedURLList()) == 0 {
		return nil, fmt.Errorf("%w: feed_urls is required", datasource.ErrInvalidCredentials)
	}
	return &cfg, nil
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
// FeedItems maps feedURL → itemID → fingerprint, where fingerprint is the
// item's updated/published timestamp (RFC3339) or, when no timestamp exists,
// a "h:<sha256-prefix>" content hash. An item is considered unchanged when its
// fingerprint matches the stored one.
type rssCursor struct {
	LastSyncTime time.Time                    `json:"last_sync_time"`
	FeedItems    map[string]map[string]string `json:"feed_items,omitempty"`
}

// itemFingerprint returns a change-detection token for a feed item. It prefers
// the explicit updated/published timestamp; absent that, it hashes the content
// so edits to timestamp-less items are still detected.
func itemFingerprint(updated, published *time.Time, content string) string {
	if updated != nil && !updated.IsZero() {
		return updated.UTC().Format(time.RFC3339)
	}
	if published != nil && !published.IsZero() {
		return published.UTC().Format(time.RFC3339)
	}
	sum := sha256.Sum256([]byte(content))
	return "h:" + hex.EncodeToString(sum[:])[:16]
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
