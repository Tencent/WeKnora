package opds

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// Config holds OPDS-specific configuration.
//
// CatalogURLs are stored in DataSourceConfig.Settings (non-secret, editable in
// the UI without replacing credentials). Basic credentials and custom headers
// live in Credentials because they are secrets that must be encrypted at rest.
// Credentials may still carry catalog_urls for backward compatibility with
// rows written before the split.
type Config struct {
	// CatalogURLs is a newline- or comma-separated list of OPDS catalog URLs.
	CatalogURLs string `json:"catalog_urls"`

	// Username and Password are optional HTTP Basic credentials, applied only
	// to catalog hosts (never to third-party acquisition/CDN hosts).
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	// AuthHeaders is an optional newline-separated list of custom request
	// headers in "Name: Value" form, applied only to catalog hosts.
	AuthHeaders string `json:"auth_headers,omitempty"`
}

// parseConfig extracts and validates OPDS configuration.
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
		return nil, fmt.Errorf("parse opds credentials: %w", err)
	}
	if urls := catalogURLsFromSettings(config.Settings); urls != "" {
		cfg.CatalogURLs = urls
	}
	if len(cfg.catalogURLList()) == 0 {
		return nil, fmt.Errorf("%w: catalog_urls is required", datasource.ErrInvalidCredentials)
	}
	return &cfg, nil
}

func catalogURLsFromSettings(settings map[string]interface{}) string {
	if len(settings) == 0 {
		return ""
	}
	raw, ok := settings["catalog_urls"]
	if !ok {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// catalogURLList splits CatalogURLs on newlines and commas, trims, dedupes
// (order preserved), and drops blanks.
func (c *Config) catalogURLList() []string {
	if c == nil {
		return nil
	}
	raw := strings.FieldsFunc(c.CatalogURLs, func(r rune) bool {
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

// hasBasicAuth reports whether both halves of a Basic credential pair are set.
// A lone username or password is treated as unconfigured rather than sent as a
// half-formed Authorization header.
func (c *Config) hasBasicAuth() bool {
	return c != nil && strings.TrimSpace(c.Username) != "" && strings.TrimSpace(c.Password) != ""
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

// opdsCursor stores incremental sync state.
//
// Signals maps feedURL → entryID → cheap fingerprint of the entry's
// feed-visible fields, used to skip the (expensive) book download when nothing
// about the entry changed. Hashes maps feedURL → entryID → hash of the
// downloaded file bytes, so an entry whose metadata changed but whose file did
// not is not re-ingested.
type opdsCursor struct {
	LastSyncTime time.Time                    `json:"last_sync_time"`
	Signals      map[string]map[string]string `json:"signals,omitempty"`
	Hashes       map[string]map[string]string `json:"hashes,omitempty"`
}

// entrySignalFingerprint hashes the feed-visible fields that would change the
// ingested document, so an unchanged entry needs no download.
func entrySignalFingerprint(entry *opdsEntry) string {
	if entry == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(entryID(entry))
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(entry.Title))
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(entry.Updated))
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(entry.Published))
	b.WriteByte('\n')
	if l := acquisitionLink(entry); l != nil {
		b.WriteString(l.Href)
		b.WriteByte('\n')
		b.WriteString(l.Type)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "s:" + hex.EncodeToString(sum[:])[:16]
}

// contentFingerprint hashes downloaded file bytes for change detection.
func contentFingerprint(content []byte) string {
	sum := sha256.Sum256(content)
	return "h:" + hex.EncodeToString(sum[:])[:16]
}

// copyFeedCursor carries a feed's prior fingerprints into the new cursor when
// this sync could not read the feed, so a transient failure does not force a
// full re-download on the next run.
func copyFeedCursor(dst, prev *opdsCursor, feedURL string) {
	if dst == nil || prev == nil {
		return
	}
	if src, ok := prev.Signals[feedURL]; ok && len(src) > 0 {
		if dst.Signals == nil {
			dst.Signals = make(map[string]map[string]string)
		}
		dst.Signals[feedURL] = maps.Clone(src)
	}
	if src, ok := prev.Hashes[feedURL]; ok && len(src) > 0 {
		if dst.Hashes == nil {
			dst.Hashes = make(map[string]map[string]string)
		}
		dst.Hashes[feedURL] = maps.Clone(src)
	}
}

// itemExternalID scopes an entry ID to its feed so IDs cannot collide across
// catalogs.
func itemExternalID(feedURL, entryID string) string {
	return feedURL + ":" + entryID
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

// sanitizeFileName keeps catalog titles on one line before applying shared limits.
func sanitizeFileName(name string) string {
	name = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(name)
	return datasource.SanitizeFileName(strings.TrimSpace(name))
}
