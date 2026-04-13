package nutstore

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Config holds Nutstore-specific configuration.
type Config struct {
	// Username is the Nutstore account email
	Username string `json:"username"`
	// Password is the app-specific password from Nutstore settings
	Password string `json:"password"`
	// BaseURL is the WebDAV server URL without /dav suffix
	// Public: "https://dav.jianguoyun.com", Enterprise: "https://drive.{domain}.com"
	BaseURL string `json:"base_url"`
	// RootPath is the root directory to sync, e.g. "/my-documents/product-docs"
	RootPath string `json:"root_path"`
	// FileTypes limits which file types to sync. Empty = sync all types.
	// Only used at FetchAll/FetchIncremental stage, not in ListResources.
	FileTypes []string `json:"file_types"`
	// RequestIntervalMs is the delay between requests (enterprise default: 0)
	RequestIntervalMs int `json:"request_interval_ms"`
}

// FileInfo represents a file or directory from WebDAV PROPFIND response.
type FileInfo struct {
	Path         string    // Full path relative to /dav, e.g. "/product-docs/D27.pdf"
	Name         string    // File or directory name
	IsDir        bool      // Whether this is a directory
	Size         int64     // File size in bytes
	LastModified time.Time // Last modification time
	ContentType  string    // MIME type
	ETag         string    // ETag for change detection
}

// --- WebDAV XML response structures ---

// multistatusResponse is the top-level PROPFIND XML response.
type multistatusResponse struct {
	XMLName   xml.Name   `xml:"DAV: multistatus"`
	Responses []response `xml:"response"`
}

// response is a single entry in the PROPFIND response.
type response struct {
	Href     string   `xml:"href"`
	Propstat propstat `xml:"propstat"`
}

// propstat contains properties and status.
type propstat struct {
	Prop   prop   `xml:"prop"`
	Status string `xml:"status"`
}

// prop holds the WebDAV properties.
type prop struct {
	DisplayName  string       `xml:"displayname"`
	ContentLen   int64        `xml:"getcontentlength"`
	ContentType  string       `xml:"getcontenttype"`
	LastModified string       `xml:"getlastmodified"`
	ETag         string       `xml:"getetag"`
	ResourceType resourceType `xml:"resourcetype"`
}

// resourceType indicates if the resource is a collection (directory).
type resourceType struct {
	Collection *struct{} `xml:"collection"`
}

// publishResponse is the XML response from the share link API.
type publishResponse struct {
	XMLName   xml.Name `xml:"publish"`
	ShareLink string   `xml:"sharelink"`
}

// nutstoreCursor stores incremental sync state.
type nutstoreCursor struct {
	LastSyncTime time.Time `json:"last_sync_time"`
}

// parseConfig extracts and validates Nutstore configuration from DataSourceConfig.
func parseConfig(credentials map[string]any, settings map[string]any) (*Config, error) {
	cfg := &Config{}

	// Parse credentials
	credBytes, err := json.Marshal(credentials)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}
	if err := json.Unmarshal(credBytes, cfg); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	// Parse settings
	if settings != nil {
		settBytes, err := json.Marshal(settings)
		if err != nil {
			return nil, fmt.Errorf("marshal settings: %w", err)
		}
		if err := json.Unmarshal(settBytes, cfg); err != nil {
			return nil, fmt.Errorf("parse settings: %w", err)
		}
	}

	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("nutstore username and password are required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://dav.jianguoyun.com"
	}
	// Strip trailing slash from BaseURL
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	// Strip trailing /dav if user accidentally included it
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/dav")
	if cfg.RootPath == "" {
		cfg.RootPath = "/"
	}
	if !strings.HasPrefix(cfg.RootPath, "/") {
		cfg.RootPath = "/" + cfg.RootPath
	}

	return cfg, nil
}

// parseLastModified parses the WebDAV last-modified header format.
// Format: "Tue, 07 May 2024 03:35:43 GMT"
func parseLastModified(s string) time.Time {
	t, err := http.ParseTime(s)
	if err != nil {
		return time.Time{}
	}
	return t
}
