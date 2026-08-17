// Package pds implements the WeKnora data source connector for Alibaba
// Cloud PDS (Drive and Photo Service), syncing files from a PDS drive
// into the knowledge base.
//
// Two transports back the connector, selected by credential type:
//
//   - AK/SK -> the official alibabacloud-go/pds-20220301 SDK
//     (client_sdk.go), which implements the gateway's ACS3-HMAC-SHA256
//     request signing.
//   - OAuth access_token / refresh_token -> a thin raw HTTP client
//     (client.go) against the same OpenAPI, kept SDK-free so it stays
//     testable via httptest.
//
// Both transports speak the PDS OpenAPI (2022-03-01) wire format: flat
// JSON response bodies such as {"items": [...], "next_marker": "..."}.
// There is NO {code,message,data} envelope on success; errors arrive as
// non-2xx responses with a {"code","message"} JSON body.
package pds

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// Environment variable names used as defaults when the data source
// credentials map leaves a field blank. The user can override any field
// on the data source; env vars are only a deployment convenience.
const (
	EnvEndpoint        = "PDS_ENDPOINT"
	EnvAccessKeyID     = "PDS_ACCESS_KEY_ID"
	EnvAccessKeySecret = "PDS_ACCESS_KEY_SECRET"
	EnvRefreshToken    = "PDS_REFRESH_TOKEN"
	EnvDomainID        = "PDS_DOMAIN_ID"
	EnvDriveID         = "PDS_DRIVE_ID"
)

// Config is the credential + endpoint bundle parsed from
// DataSourceConfig.Credentials. Falls back to environment variables when
// a field is blank in the per-data-source UI. Secrets are encrypted at
// rest by types.DataSourceConfig's AES-256-GCM wire format.
type Config struct {
	// Endpoint is the PDS OpenAPI host without scheme. Examples:
	//   - Enterprise:  bj22856.api.aliyunfile.com
	//   - Personal:    <instance>.api.aliyunpds.com
	// Defaults to "pds.aliyuncs.com" (the public cloud default).
	Endpoint string `json:"endpoint,omitempty"`

	// AccessToken is the bearer token used to authenticate PDS API calls.
	// If the user supplies a RefreshToken instead, the client exchanges it
	// for an AccessToken on first use and re-exchanges it when the server
	// reports the token as expired/invalid.
	AccessToken string `json:"access_token,omitempty"`

	// AccessKeyID / AccessKeySecret are Alibaba Cloud static credentials,
	// an alternative to the OAuth RefreshToken. When present, requests are
	// routed through the official SDK (ACS3-HMAC-SHA256 signing).
	AccessKeyID     string `json:"access_key_id,omitempty"`
	AccessKeySecret string `json:"access_key_secret,omitempty"`

	// RefreshToken is the long-lived OAuth token from the PDS app console.
	// When supplied, the client calls PDS to exchange it for an AccessToken
	// on first use.
	RefreshToken string `json:"refresh_token,omitempty"`

	// DomainID scopes the data source to a single PDS domain (tenant).
	// For enterprise endpoints the domain is usually derivable from the
	// endpoint subdomain; we still require it to be explicit so it can be
	// recorded in sync metadata and log output.
	DomainID string `json:"domain_id,omitempty"`
}

// GetEndpoint returns the normalized PDS endpoint URL (with scheme). When
// the user supplies a bare host we default to https://. Tests inject a
// full URL like "http://127.0.0.1:port" — we honor that scheme so the
// fake server is reachable.
func (c *Config) GetEndpoint() string {
	e := strings.TrimSpace(c.Endpoint)
	if e == "" {
		return "https://pds.aliyuncs.com"
	}
	if !strings.Contains(e, "://") {
		e = "https://" + e
	}
	return strings.TrimRight(e, "/")
}

// GetHost returns just the host (no scheme) of the PDS endpoint, used as
// the SSRF-validation key.
func (c *Config) GetHost() string {
	e := c.GetEndpoint()
	if i := strings.Index(e, "://"); i >= 0 {
		return strings.TrimRight(e[i+3:], ".")
	}
	return strings.TrimRight(e, ".")
}

// IsConfigured reports whether the config has at least one usable auth path.
func (c *Config) IsConfigured() bool {
	return c.AccessToken != "" || c.RefreshToken != "" ||
		(c.AccessKeyID != "" && c.AccessKeySecret != "")
}

// parseConfig decodes DataSourceConfig.Credentials into Config, layering
// environment-variable defaults under the user-supplied values.
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
		// Credentials may not be a struct (e.g. empty map). Start with an
		// empty Config and rely on the env defaults below.
		cfg = Config{}
	}

	// Layer env defaults under user-provided values: only set if blank.
	if cfg.Endpoint == "" {
		cfg.Endpoint = os.Getenv(EnvEndpoint)
	}
	if cfg.AccessKeyID == "" {
		cfg.AccessKeyID = os.Getenv(EnvAccessKeyID)
	}
	if cfg.AccessKeySecret == "" {
		cfg.AccessKeySecret = os.Getenv(EnvAccessKeySecret)
	}
	if cfg.RefreshToken == "" {
		cfg.RefreshToken = os.Getenv(EnvRefreshToken)
	}
	if cfg.DomainID == "" {
		cfg.DomainID = os.Getenv(EnvDomainID)
	}

	if strings.TrimSpace(cfg.DomainID) == "" {
		return nil, fmt.Errorf("%w: domain_id is required", datasource.ErrInvalidCredentials)
	}
	if !cfg.IsConfigured() {
		return nil, fmt.Errorf(
			"%w: one of access_token, refresh_token, or (access_key_id + access_key_secret) is required",
			datasource.ErrInvalidCredentials)
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.GetEndpoint()); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Settings is the unencrypted, non-secret configuration block stored in
// DataSourceConfig.Settings. The drive_id is required for sync; the
// picker UI persists the chosen drive into ResourceIDs as a fallback.
type Settings struct {
	// DriveID is the PDS drive to sync. Usually left blank — the picker
	// selection (ResourceIDs) is the source of truth; this field lets an
	// admin pin a drive at the data-source level.
	DriveID string `json:"drive_id,omitempty"`

	// FileTypes is an extension whitelist (lowercase, with leading dot).
	// Empty means "all supported types".
	FileTypes []string `json:"file_types,omitempty"`
}

// parseSettings decodes DataSourceConfig.Settings into Settings.
func parseSettings(config *types.DataSourceConfig) Settings {
	var s Settings
	if config != nil && config.Settings != nil {
		raw, err := json.Marshal(config.Settings)
		if err == nil {
			_ = json.Unmarshal(raw, &s)
		}
	}
	return s
}

// DriveIDFromConfigOrEnv returns the drive_id, falling back to the
// PDS_DRIVE_ID env var and finally to the first resource_id the user
// picked in the resource tree when both the setting and the env var are
// blank.
//
// Why the resource_ids fallback: the picker writes the chosen drive's
// ExternalID into ResourceIDs — the resource tree is the user's source of
// truth for "which drive". Settings.drive_id is a convenience duplicate
// that lets admins override at the data-source level.
//
// The picker has two storage shapes for a drive selection:
//
//	"<driveID>:" — the connector's canonical drive resourceID
//	"<driveID>"  — a bare drive ID (legacy selections)
//
// We accept either.
func DriveIDFromConfigOrEnv(config *types.DataSourceConfig) string {
	if config == nil {
		return os.Getenv(EnvDriveID)
	}
	if s := parseSettings(config); s.DriveID != "" {
		return s.DriveID
	}
	if v := os.Getenv(EnvDriveID); v != "" {
		return v
	}
	for _, rid := range config.ResourceIDs {
		rid = strings.TrimSpace(rid)
		if rid == "" {
			continue
		}
		if driveID, _ := splitPDSResourceID(rid); driveID != "" {
			// "<driveID>:<fileID>" or "<driveID>:" — accept even when a
			// fileID is present (the user picked a folder/file inside the
			// drive); sync is drive-scoped.
			return driveID
		}
		// No colon — treat the whole token as a drive ID.
		return rid
	}
	return ""
}

// IsSupportedFile reports whether the given file passes the file_types
// whitelist. Empty whitelist means everything is allowed. Files without a
// name (some delta events only carry a file_id) always pass — we cannot
// apply an extension filter without a name, and skipping them would leak
// deletes/updates past the filter silently.
func (s Settings) IsSupportedFile(name string) bool {
	if len(s.FileTypes) == 0 || name == "" {
		return true
	}
	ext := strings.ToLower(fileExt(name))
	for _, ft := range s.FileTypes {
		if strings.ToLower(strings.TrimSpace(ft)) == ext {
			return true
		}
	}
	return false
}

func fileExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

// pdsDrive is the wire shape of a PDS drive (a.k.a. space) as returned by
// POST /v2/drive/list. Field names mirror the official SDK's Drive model.
type pdsDrive struct {
	DriveID     string    `json:"drive_id"`
	Name        string    `json:"drive_name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"-"`
	// CreatedAtRaw carries the RFC3339 string PDS returns; CreatedAt is
	// the best-effort parse of it (zero when absent/unparseable).
	CreatedAtRaw string `json:"created_at,omitempty"`
}

// UnmarshalJSON parses created_at best-effort into CreatedAt.
func (d *pdsDrive) UnmarshalJSON(b []byte) error {
	type alias pdsDrive
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*d = pdsDrive(a)
	if d.CreatedAtRaw != "" {
		if t, err := time.Parse(time.RFC3339, d.CreatedAtRaw); err == nil {
			d.CreatedAt = t
		}
	}
	return nil
}

// pdsFile is the wire shape of a PDS file or folder entry as returned by
// POST /v2/file/list (and nested inside delta items). Field names mirror
// the official SDK's File model — notably "parent_file_id", NOT
// "parent_id", and "download_url", NOT "url".
type pdsFile struct {
	FileID      string    `json:"file_id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"` // "file" | "folder"
	ParentID    string    `json:"parent_file_id,omitempty"`
	NamePath    string    `json:"name_path,omitempty"`
	URL         string    `json:"download_url,omitempty"`
	Size        int64     `json:"size,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	UpdatedAt   time.Time `json:"-"`
	// UpdatedAtRaw carries the RFC3339 string PDS returns; UpdatedAt is
	// the best-effort parse of it.
	UpdatedAtRaw string `json:"updated_at,omitempty"`

	// FilePath is the readable path relative to the drive root, stamped
	// by the recursive ListFile walker (listAllFiles). Examples:
	//   "pgsql.md"          (drive root file)
	//   "/02/pgsql.md"      (nested file)
	//   "/02/"              (folder, trailing slash)
	// Not populated from wire JSON.
	FilePath string `json:"-"`
}

// UnmarshalJSON parses updated_at best-effort into UpdatedAt.
func (f *pdsFile) UnmarshalJSON(b []byte) error {
	type alias pdsFile
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*f = pdsFile(a)
	if f.UpdatedAtRaw != "" {
		if t, err := time.Parse(time.RFC3339, f.UpdatedAtRaw); err == nil {
			f.UpdatedAt = t
		}
	}
	return nil
}

// pdsDeltaItem is one entry of the POST /v2/file/list_delta response: the
// changed file plus the operation that changed it. Op is one of
// "create", "overwrite", "update", "move", "delete" — deletion MUST be
// detected via this field: the delta feed only returns items that changed
// since the cursor, so absence from a delta page says nothing about
// whether a file still exists.
type pdsDeltaItem struct {
	File   pdsFile `json:"file"`
	FileID string  `json:"file_id,omitempty"`
	Op     string  `json:"op,omitempty"`
}

// isDelete reports whether the delta item is a deletion event.
func (d pdsDeltaItem) isDelete() bool { return strings.EqualFold(d.Op, "delete") }

// resolvedFile returns the item's file, falling back to the bare file_id
// for events whose file object is empty (PDS delete events sometimes
// carry only the id).
func (d pdsDeltaItem) resolvedFile() pdsFile {
	f := d.File
	if f.FileID == "" {
		f.FileID = d.FileID
	}
	return f
}

// pdsFileToResource converts a PDS file/folder into the picker Resource.
// ExternalIDs use the "<driveID>:<fileID>" encoding so the whole picker
// tree is scoped per drive. Root-level entries point their ParentID at the
// drive node's own resourceID ("<driveID>:") — the frontend groups
// children by matching parent_id against the parent node's external_id,
// so this MUST stay consistent with ListResources' root encoding.
func pdsFileToResource(f pdsFile, driveID string) types.Resource {
	hasChildren := strings.EqualFold(f.Type, "folder")
	parent := f.ParentID
	if parent == "" || strings.EqualFold(parent, "root") {
		parent = "" // drive root -> the drive node's resourceID
	}
	return types.Resource{
		ExternalID:  pdsResourceID(driveID, f.FileID),
		Name:        f.Name,
		Type:        f.Type,
		URL:         f.URL,
		ParentID:    pdsResourceID(driveID, parent),
		HasChildren: hasChildren,
		ModifiedAt:  f.UpdatedAt,
		Metadata: map[string]interface{}{
			"drive_id": driveID,
			"file_id":  f.FileID,
		},
	}
}

// pdsResourceID encodes a file/folder reference as "<driveID>:<fileID>".
// With an empty fileID it yields the drive's own resourceID "<driveID>:".
// The driveID prefix lets ResolveResourceAncestors scope the walk to a
// single drive without an extra round-trip.
func pdsResourceID(driveID, fileID string) string {
	if driveID == "" {
		return fileID
	}
	return driveID + ":" + fileID
}

// splitPDSResourceID returns (driveID, fileID) from a picker resourceID.
// A token without a colon yields ("", token); callers decide whether the
// bare token is a drive ID (picker root selections) or malformed.
func splitPDSResourceID(rid string) (string, string) {
	if i := strings.Index(rid, ":"); i >= 0 {
		return rid[:i], rid[i+1:]
	}
	return "", rid
}

// MarshalJSON emits created_at as RFC3339, derived from CreatedAt when
// the raw string is unset. Keeps fake-server round-trips natural: tests
// set the time field, the wire carries the string.
func (d pdsDrive) MarshalJSON() ([]byte, error) {
	type alias pdsDrive
	a := alias(d)
	if a.CreatedAtRaw == "" && !a.CreatedAt.IsZero() {
		a.CreatedAtRaw = a.CreatedAt.UTC().Format(time.RFC3339)
	}
	return json.Marshal(a)
}

// MarshalJSON emits updated_at as RFC3339, derived from UpdatedAt when
// the raw string is unset. See pdsDrive.MarshalJSON.
func (f pdsFile) MarshalJSON() ([]byte, error) {
	type alias pdsFile
	a := alias(f)
	if a.UpdatedAtRaw == "" && !a.UpdatedAt.IsZero() {
		a.UpdatedAtRaw = a.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return json.Marshal(a)
}

// buildFolderPathMap walks a flat listing of files/folders and returns a
// map from folder-fileID -> readable path ("/A/B" style). We do a DFS
// over folder entries, building the path recursively from the parent
// chain.
//
// Top-level folders (parent "root", "", or unknown) get a path of
// "/Name". Files can then look up their parent folder via
// folderPath[parentFileID]. Cycle-safe via a pre-mark guard so a bad
// response with a self-referential parent doesn't infinite-loop.
func buildFolderPathMap(entries []pdsFile) map[string]string {
	folderByID := make(map[string]pdsFile)
	for _, e := range entries {
		if strings.EqualFold(e.Type, "folder") {
			folderByID[e.FileID] = e
		}
	}
	resolved := make(map[string]string, len(folderByID))
	var resolve func(pdsFile, int) string
	resolve = func(f pdsFile, depth int) string {
		if depth > 32 {
			// Pathological nesting — bail out rather than recurse forever.
			return "/" + f.Name
		}
		if p, ok := resolved[f.FileID]; ok {
			return p
		}
		parentID := f.ParentID
		if parentID == "" || strings.EqualFold(parentID, "root") {
			resolved[f.FileID] = "/" + f.Name
			return resolved[f.FileID]
		}
		parent, isKnown := folderByID[parentID]
		if !isKnown {
			// Parent isn't in the listing — either this folder is at the
			// drive root or the listing is incomplete. Treat as root.
			resolved[f.FileID] = "/" + f.Name
			return resolved[f.FileID]
		}
		// Mark before recursing to break cycles.
		resolved[f.FileID] = ""
		parentPath := resolve(parent, depth+1)
		full := parentPath + "/" + f.Name
		resolved[f.FileID] = full
		return full
	}
	for _, f := range folderByID {
		_ = resolve(f, 0)
	}
	return resolved
}

// fileFolderPath returns the readable folder path for a file given the
// folder map from buildFolderPathMap. Returns "" for files at the drive
// root or whose parent folder is unknown.
func fileFolderPath(f pdsFile, folderPath map[string]string) string {
	parentID := f.ParentID
	if parentID == "" || strings.EqualFold(parentID, "root") {
		return ""
	}
	if p, ok := folderPath[parentID]; ok {
		return p
	}
	return ""
}

// baseMetadata builds the FetchedItem.Metadata map carried on tombstones
// and other fileless items. The "channel" key is required because the
// search-log join uses it.
func baseMetadata(driveID, fileID, url string) map[string]string {
	return map[string]string{
		"channel":      types.ChannelPDS,
		"source_type":  types.ChannelPDS,
		"pds_drive_id": driveID,
		"pds_file_id":  fileID,
		"pds_url":      url,
	}
}

// baseMetadataWithFolder is the file-emission variant: it includes the
// readable path and parent_file_id so search and downstream tools can
// group results by folder.
//
// Path resolution order:
//  1. f.FilePath stamped by the recursive listAllFiles walker — preferred.
//     Drive-root files have FilePath == "<name>" (no leading slash) → we
//     omit pds_path/pds_folder. Nested files look like "/folder/name.pdf".
//  2. f.NamePath as reported by the PDS API (delta events carry it).
//  3. folderPath map lookup for callers that only have a flat listing.
func baseMetadataWithFolder(driveID string, f pdsFile, folderPath map[string]string, url string) map[string]string {
	var path string
	switch {
	case f.FilePath != "":
		path = f.FilePath
	case f.NamePath != "":
		path = f.NamePath
	default:
		path = fileFolderPath(f, folderPath)
	}
	m := map[string]string{
		"channel":      types.ChannelPDS,
		"source_type":  types.ChannelPDS,
		"pds_drive_id": driveID,
		"pds_file_id":  f.FileID,
		"pds_url":      url,
	}
	// Only emit pds_path / pds_folder when the file actually lives inside
	// a folder (i.e. path has a "/"). Drive-root files carry a bare name.
	if i := strings.LastIndex(path, "/"); i >= 0 {
		// pds_path is the full file path including basename (e.g.
		// "/02/pgsql.md") so downstream tools can reconstruct the exact
		// location.
		m["pds_path"] = path
		// pds_folder is the folder portion only — the column the KB
		// sidebar tree groups documents by.
		m["pds_folder"] = path[:i]
	}
	if f.ParentID != "" && !strings.EqualFold(f.ParentID, "root") {
		m["pds_parent_file_id"] = f.ParentID
	}
	return m
}
