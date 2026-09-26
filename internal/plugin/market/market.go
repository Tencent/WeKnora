// Package market reads a plugin marketplace index: a JSON document listing
// plugins and, per version, where the package is and its digest. A system
// administrator browses it and installs through the usual review, pinned to
// the listed digest, so a package that changed since it was listed is
// refused.
//
// The index says nothing about trust: a package is only as trusted as its
// signature (see the trust package). A marketplace that reviews plugins
// signs them with its key, which platforms add to their trust store.
package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/utils"
)

// SchemaVersion is the index format this package reads.
const SchemaVersion = 1

// maxIndexBytes bounds the index document.
const maxIndexBytes = 8 << 20

// Index is the marketplace document.
type Index struct {
	SchemaVersion int     `json:"schemaVersion"`
	Plugins       []Entry `json:"plugins"`
}

// Entry is one plugin in the index.
type Entry struct {
	ID          string                 `json:"id"`
	Name        manifest.LocalizedText `json:"name"`
	Description manifest.LocalizedText `json:"description,omitzero"`
	Publisher   manifest.Publisher     `json:"publisher"`
	// Icon is an https URL or a data: URI.
	Icon       string    `json:"icon,omitempty"`
	Homepage   string    `json:"homepage,omitempty"`
	Categories []string  `json:"categories,omitempty"`
	Versions   []Version `json:"versions"`
}

// Version is one published package of a plugin.
type Version struct {
	Version string `json:"version"`
	// URL is where the package is, absolute or relative to the index.
	URL string `json:"url"`
	// Digest is "sha256:<hex>" of the package archive.
	Digest  string           `json:"digest"`
	Size    int64            `json:"size,omitempty"`
	Engines manifest.Engines `json:"engines,omitzero"`
	// Runtime is the package's runtime type, for display.
	Runtime string `json:"runtime,omitempty"`
}

// Listing is an entry as this platform sees it.
type Listing struct {
	Entry
	// Latest is the newest version this WeKnora can run; nil when none can.
	Latest *Version `json:"latest"`
	// Incompatible says why no version can run here.
	Incompatible string `json:"incompatible,omitempty"`
}

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.[a-z0-9][a-z0-9.-]*$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Client reads one index.
type Client struct {
	indexURL    string
	hostVersion string
	http        *http.Client
}

// FromEnv is the client for WEKNORA_PLUGIN_INDEX_URL, or nil when unset.
func FromEnv(hostVersion string) *Client {
	raw := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_INDEX_URL"))
	if raw == "" {
		return nil
	}
	cfg := utils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = 20 * time.Second
	return New(raw, hostVersion, utils.NewSSRFSafeHTTPClient(cfg))
}

// New creates a client.
func New(indexURL, hostVersion string, client *http.Client) *Client {
	return &Client{indexURL: indexURL, hostVersion: hostVersion, http: client}
}

// URL is the index address.
func (c *Client) URL() string { return c.indexURL }

// List fetches the index and returns the entries that are well formed,
// each with the newest version this WeKnora can run. Malformed entries are
// left out and reported in skipped.
func (c *Client) List(ctx context.Context) (listings []Listing, skipped []string, err error) {
	base, err := url.Parse(c.indexURL)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		return nil, nil, fmt.Errorf("WEKNORA_PLUGIN_INDEX_URL must be an http(s) URL")
	}
	if err := utils.ValidateURLForSSRF(c.indexURL); err != nil {
		return nil, nil, fmt.Errorf("the index URL is not allowed (private hosts must be in SSRF_WHITELIST): %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.indexURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch the plugin index: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("fetch the plugin index: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read the plugin index: %w", err)
	}
	if len(data) > maxIndexBytes {
		return nil, nil, fmt.Errorf("the plugin index is over %d bytes", maxIndexBytes)
	}
	return Parse(data, base, c.hostVersion)
}

// Parse reads an index document fetched from base.
func Parse(data []byte, base *url.URL, hostVersion string) (listings []Listing, skipped []string, err error) {
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, nil, fmt.Errorf("the plugin index is not valid JSON: %w", err)
	}
	if idx.SchemaVersion != SchemaVersion {
		return nil, nil, fmt.Errorf("the plugin index has schemaVersion %d; this WeKnora reads %d",
			idx.SchemaVersion, SchemaVersion)
	}
	seen := map[string]bool{}
	for _, e := range idx.Plugins {
		if err := normalize(&e, base); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", e.ID, err))
			continue
		}
		if seen[e.ID] {
			skipped = append(skipped, fmt.Sprintf("%s: listed twice", e.ID))
			continue
		}
		seen[e.ID] = true
		listings = append(listings, listing(e, hostVersion))
	}
	sort.Slice(listings, func(i, j int) bool { return listings[i].ID < listings[j].ID })
	return listings, skipped, nil
}

// normalize checks an entry and resolves its URLs against the index.
func normalize(e *Entry, base *url.URL) error {
	if !idPattern.MatchString(e.ID) {
		return fmt.Errorf("id is not a plugin ID")
	}
	if e.Name.IsZero() {
		e.Name = manifest.Text(e.ID, nil)
	}
	if e.Icon != "" && !strings.HasPrefix(e.Icon, "data:image/") && !strings.HasPrefix(e.Icon, "https://") {
		e.Icon = ""
	}
	if len(e.Versions) == 0 {
		return fmt.Errorf("no versions")
	}
	for i := range e.Versions {
		v := &e.Versions[i]
		if !semver.IsValid("v" + v.Version) {
			return fmt.Errorf("version %q is not semver", v.Version)
		}
		if !digestPattern.MatchString(v.Digest) {
			return fmt.Errorf("version %s: digest must be sha256:<64 hex>", v.Version)
		}
		ref, err := url.Parse(v.URL)
		if err != nil || v.URL == "" {
			return fmt.Errorf("version %s: bad url", v.Version)
		}
		abs := base.ResolveReference(ref)
		if abs.Scheme != "https" && abs.Scheme != "http" {
			return fmt.Errorf("version %s: url must be http(s)", v.Version)
		}
		v.URL = abs.String()
	}
	sort.Slice(e.Versions, func(i, j int) bool {
		return semver.Compare("v"+e.Versions[i].Version, "v"+e.Versions[j].Version) > 0
	})
	return nil
}

// listing picks the newest version the host can run.
func listing(e Entry, hostVersion string) Listing {
	l := Listing{Entry: e}
	var why string
	for i := range e.Versions {
		v := e.Versions[i]
		m := manifest.Manifest{Engines: v.Engines}
		if err := m.CheckEngines(hostVersion); err != nil {
			if why == "" {
				why = err.Error()
			}
			continue
		}
		l.Latest = &v
		return l
	}
	l.Incompatible = why
	return l
}
