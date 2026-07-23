// Package confluence implements the Atlassian Confluence 7.x data source connector for WeKnora.
//
// It syncs pages from Confluence spaces into WeKnora knowledge bases by exporting
// each page as a PDF via Confluence's built-in PDF export action.
//
// Confluence 7.x REST API docs:
//   - Auth:       Basic Auth (username + password)
//   - Spaces:     GET /rest/api/space
//   - Content:    GET /rest/api/content/{id}?expand=body.storage,version,space
//   - Children:   GET /rest/api/content/{id}/child/page
//   - Search:     GET /rest/api/content/search?cql=...
//   - PDF export: GET /spaces/flyingpdf/pdfpageexport.action?pageId={id}
//
// This connector targets Confluence Server / Data Center 7.x specifically.
// Cloud and 8.x+ may differ in API paths or PDF export mechanism.
package confluence

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// Config holds Confluence-specific configuration for the data source connector.
type Config struct {
	// Edition distinguishes Confluence Server / Data Center ("server") from
	// Confluence Cloud ("cloud").  Defaults to "server" when empty so that
	// existing rows created before this field remain compatible.
	Edition string `json:"edition"`

	// Base URL of the Confluence instance (e.g. "https://confluence.example.com"
	// for Server, "https://your-domain.atlassian.net/wiki" for Cloud)
	BaseURL string `json:"base_url"`

	// Username for authentication.
	// Server: username for Basic Auth.
	// Cloud:  email address for Basic Auth.
	Username string `json:"username"`

	// Password for Server edition (Basic Auth password).
	Password string `json:"password"`

	// APIToken for Cloud edition (Atlassian API token, used as password in
	// Basic Auth together with the email).
	APIToken string `json:"api_token"`
}

// IsCloud reports whether this config targets Confluence Cloud.
func (c *Config) IsCloud() bool {
	return c.Edition == "cloud"
}

// parseConfluenceConfig extracts and validates Confluence config from DataSourceConfig.
func parseConfluenceConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, datasource.ErrInvalidConfig
	}

	creds := config.Credentials

	edition, _ := creds["edition"].(string)
	if edition == "" {
		edition = "server"
	}

	baseURL, _ := creds["base_url"].(string)
	if baseURL == "" {
		return nil, fmt.Errorf("%w: missing base_url", datasource.ErrInvalidCredentials)
	}

	username, _ := creds["username"].(string)
	if username == "" {
		return nil, fmt.Errorf("%w: missing username", datasource.ErrInvalidCredentials)
	}

	cfg := &Config{
		Edition:  edition,
		BaseURL:  baseURL,
		Username: username,
	}

	switch edition {
	case "cloud":
		apiToken, _ := creds["api_token"].(string)
		if apiToken == "" {
			return nil, fmt.Errorf("%w: missing api_token", datasource.ErrInvalidCredentials)
		}
		cfg.APIToken = apiToken
	default: // "server"
		password, _ := creds["password"].(string)
		if password == "" {
			return nil, fmt.Errorf("%w: missing password", datasource.ErrInvalidCredentials)
		}
		cfg.Password = password
	}

	return cfg, nil
}

// --- API response types ---

// confluenceSpace represents a Confluence space from GET /rest/api/space (v1).
// Used by Server / Data Center edition.
// confluenceSpace represents a Confluence space.
// ID is stored as a string to accommodate both Server (numeric) and Cloud (string) formats.
type confluenceSpace struct {
	ID       string `json:"-"` // set programmatically; v1 API returns int, converted to string
	Key      string `json:"key"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Homepage struct {
		ID string `json:"id"`
	} `json:"homepage"`
	Description struct {
		View struct {
			Value string `json:"value"`
		} `json:"view"`
	} `json:"description"`
	Links struct {
		WebUI string `json:"webui"`
		Self  string `json:"self"`
	} `json:"_links"`
}

// confluenceSpaceListResponse is the response from GET /rest/api/space (v1).
// The v1 API returns space IDs as integers, so we use a dedicated raw type
// and convert to the common confluenceSpace (string ID) in listSpacesV1.
type confluenceSpaceListResponse struct {
	Results []confluenceSpaceV1 `json:"results"`
	Start   int                 `json:"start"`
	Limit   int                 `json:"limit"`
	Size    int                 `json:"size"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// confluenceSpaceV1 is the raw space type from the v1 API where ID is an integer.
type confluenceSpaceV1 struct {
	ID       int    `json:"id"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Homepage struct {
		ID int `json:"id"` // v1 API returns integer
	} `json:"homepage"`
	Description struct {
		View struct {
			Value string `json:"value"`
		} `json:"view"`
	} `json:"description"`
	Links struct {
		WebUI string `json:"webui"`
		Self  string `json:"self"`
	} `json:"_links"`
}

// confluenceSpaceV2 represents a Confluence space from GET /api/v2/spaces.
// Used by Cloud edition. Key difference: ID is a string, not int.
type confluenceSpaceV2 struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	AuthorID    string `json:"authorId"`
	CreatedAt   string `json:"createdAt"`
	HomepageID  string `json:"homepageId"`
	Description string `json:"description"`
	Links       struct {
		WebUI string `json:"webui"`
		Self  string `json:"self"`
	} `json:"_links"`
}

// confluenceSpaceV2ListResponse is the response from GET /api/v2/spaces.
// Uses cursor-based pagination.
type confluenceSpaceV2ListResponse struct {
	Results []confluenceSpaceV2 `json:"results"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// confluencePage represents a Confluence page/content object.
type confluencePage struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Space  struct {
		ID   int    `json:"id"`
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"space"`
	Version struct {
		By struct {
			DisplayName string `json:"displayName"`
		} `json:"by"`
		When string `json:"when"`
	} `json:"version"`
	Ancestors []struct {
		ID string `json:"id"`
	} `json:"ancestors"`
	Links struct {
		WebUI string `json:"webui"`
		Self  string `json:"self"`
	} `json:"_links"`
}

// confluenceContentListResponse is the response from content listing APIs.
type confluenceContentListResponse struct {
	Results []confluencePage `json:"results"`
	Start   int              `json:"start"`
	Limit   int              `json:"limit"`
	Size    int              `json:"size"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// confluencePageWithExportView extends the basic page type with body.export_view
// for PDF rendering (used by Cloud edition where the legacy PDF export endpoint
// is unavailable).  The export_view body contains cleaner HTML than body.storage.
type confluencePageWithExportView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
	Body  struct {
		ExportView struct {
			Value          string `json:"value"`
			Representation string `json:"representation"`
		} `json:"export_view"`
	} `json:"body"`
	Version struct {
		By struct {
			DisplayName string `json:"displayName"`
		} `json:"by"`
		When string `json:"when"`
	} `json:"version"`
	Space struct {
		ID   int    `json:"id"`
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"space"`
	Links struct {
		WebUI string `json:"webui"`
		Self  string `json:"self"`
	} `json:"_links"`
}

// confluenceAttachmentResponse is the response from
// GET /rest/api/content/{id}/child/attachment.
type confluenceAttachmentResponse struct {
	Results []confluenceAttachment `json:"results"`
	Size    int                    `json:"size"`
}

// confluenceAttachment represents a single attachment on a Confluence page.
type confluenceAttachment struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// confluencePageV2 represents a page from the Cloud v2 API
// (GET /api/v2/spaces/{id}/pages). Lighter than v1 confluencePage:
// no space/version/ancestors — version is fetched separately via GetPage.
type confluencePageV2 struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Title     string `json:"title"`
	SpaceID   string `json:"spaceId"`
	CreatedAt string `json:"createdAt"`
	Version   struct {
		Number    int    `json:"number"`
		CreatedAt string `json:"createdAt"`
	} `json:"version"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

// confluencePageV2ListResponse is the paginated response from
// GET /api/v2/spaces/{id}/pages (Cloud, cursor-based).
type confluencePageV2ListResponse struct {
	Results []confluencePageV2 `json:"results"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// confluenceSearchResponse is the response from GET /rest/api/content/search.
type confluenceSearchResponse struct {
	Results []confluencePage `json:"results"`
	Start   int              `json:"start"`
	Limit   int              `json:"limit"`
	Size    int              `json:"size"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// confluenceCursor stores incremental sync state for Confluence.
// Follows the same per-resource pattern as Feishu's SpaceNodeTimes and Yuque's BookDocTimes.
type confluenceCursor struct {
	// LastSyncTime is the timestamp of the last successful sync.
	LastSyncTime time.Time `json:"last_sync_time"`

	// SpacePageTimes maps resource_id -> (page_id -> version.when).
	// Used to detect which pages have changed since last sync.
	SpacePageTimes map[string]map[string]string `json:"space_page_times,omitempty"`
}

// normalizeLastModified converts Confluence ISO 8601 time to a comparable string.
// Input: "2026-07-16T11:25:00.000+08:00" → Output: "2026-07-16 11:25"
func normalizeLastModified(isoStr string) string {
	if len(isoStr) < 16 {
		return isoStr
	}
	// Take first 16 chars "2026-07-16T11:25" and replace T with space
	result := isoStr[:16]
	result = result[:10] + " " + result[11:]
	return result
}

// parseConfluenceTimestamp attempts to parse a Confluence timestamp string.
func parseConfluenceTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t
	}
	// Try the normalized format "2006-01-02 15:04"
	if t, err := time.Parse("2006-01-02 15:04", normalizeLastModified(ts)); err == nil {
		return t
	}
	return time.Time{}
}

// safeFilename sanitizes a string for use as a filename.
func safeFilename(name string) string {
	if name == "" {
		return "untitled"
	}
	// Remove or replace unsafe characters
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 32 || c == 127 {
			continue
		}
		switch c {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			result = append(result, '_')
		default:
			result = append(result, c)
		}
	}
	s := string(result)
	// Truncate to reasonable length (UTF-8 safe)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// spaceToResource converts a Confluence space to a Resource for UI listing.
// HasChildren is false: spaces are directly selectable as a whole,
// no page-level expansion is needed — we sync the entire space.
func spaceToResource(space confluenceSpace, baseURL string) types.Resource {
	return types.Resource{
		ExternalID:  space.ID,
		Name:        space.Name,
		Type:        "space",
		URL:         baseURL + space.Links.WebUI,
		Description: space.Description.View.Value,
		HasChildren: false,
		Metadata: map[string]interface{}{
			"space_key": space.Key,
			"space_id":  space.ID,
		},
	}
}

// unmarshalCursor converts the generic SyncCursor.ConnectorCursor map to a confluenceCursor.
func unmarshalCursor(m map[string]interface{}) *confluenceCursor {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	var c confluenceCursor
	_ = json.Unmarshal(b, &c)
	return &c
}

// toSyncCursor converts the internal confluenceCursor to the generic SyncCursor wrapper.
func (c *confluenceCursor) toSyncCursor() *types.SyncCursor {
	cursorMap := make(map[string]interface{})
	b, _ := json.Marshal(c)
	_ = json.Unmarshal(b, &cursorMap)
	return &types.SyncCursor{
		LastSyncTime:    c.LastSyncTime,
		ConnectorCursor: cursorMap,
	}
}
