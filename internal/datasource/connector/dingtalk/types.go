// Package dingtalk implements the DingTalk data source connector for WeKnora.
package dingtalk

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	defaultBaseURL = "https://api.dingtalk.com"

	resourceKindSpace = "space"
	resourceKindFile  = "file"
	resourceSeparator = ":"

	maxDriveEntries = 10000
	maxFileBytes    = 50 * 1024 * 1024
)

// Config holds DingTalk connector credentials.
type Config struct {
	ClientID  string `json:"client_id"`
	AppSecret string `json:"app_secret"`
	UnionID   string `json:"union_id"`
	BaseURL   string `json:"base_url,omitempty"`
}

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
		return nil, fmt.Errorf("parse dingtalk credentials: %w", err)
	}
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.AppSecret = strings.TrimSpace(cfg.AppSecret)
	cfg.UnionID = strings.TrimSpace(cfg.UnionID)
	if cfg.ClientID == "" || cfg.AppSecret == "" || cfg.UnionID == "" {
		return nil, fmt.Errorf("%w: client_id, app_secret and union_id are required", datasource.ErrInvalidCredentials)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if err := datasource.ValidateConnectorBaseURL(cfg.BaseURL); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type space struct {
	ID   string
	Name string
}

type driveEntry struct {
	ID         string
	ParentID   string
	Name       string
	Path       string
	Type       string
	MediaType  string
	Size       int64
	ModifiedAt time.Time
	Version    string
	URL        string
	DocKey     string
}

func (e driveEntry) isFolder() bool {
	value := strings.ToLower(e.Type + " " + e.MediaType)
	return strings.Contains(value, "folder") || strings.Contains(value, "directory")
}

func (e driveEntry) sourceURL(spaceID string) string {
	if strings.TrimSpace(e.URL) != "" {
		return e.URL
	}
	return "https://alidocs.dingtalk.com/i/drive/" + spaceID + "/" + e.ID
}

func (e driveEntry) displayPath() string {
	if strings.TrimSpace(e.Path) != "" {
		return e.Path
	}
	return e.Name
}

func (e driveEntry) signal() string {
	parts := []string{
		e.ID,
		e.Version,
		fmt.Sprintf("%d", e.Size),
	}
	if !e.ModifiedAt.IsZero() {
		parts = append(parts, e.ModifiedAt.UTC().Format(time.RFC3339Nano))
	}
	return strings.Join(parts, "|")
}

func (e driveEntry) isOnlineDocument() bool {
	name := strings.ToLower(e.Name)
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".pdf" || ext == ".doc" || ext == ".docx" || ext == ".txt" ||
		ext == ".md" || ext == ".markdown" || isImageExt(ext) {
		return false
	}
	media := strings.ToLower(e.MediaType)
	typ := strings.ToLower(e.Type)
	if strings.HasPrefix(media, "image/") || strings.Contains(media, "pdf") ||
		strings.Contains(media, "wordprocessingml") || strings.HasPrefix(media, "text/") {
		return false
	}
	return media == "alidoc" || media == "adoc" || strings.Contains(media, "dingtalk") ||
		strings.Contains(typ, "doc") || strings.Contains(typ, "sheet") ||
		strings.Contains(typ, "wiki") || strings.Contains(typ, "online")
}

func supportedFile(name, mediaType string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	media := strings.ToLower(mediaType)
	switch ext {
	case ".pdf", ".doc", ".docx", ".txt", ".md", ".markdown", ".csv",
		".xls", ".xlsx", ".ppt", ".pptx":
		return true
	}
	return isImageExt(ext) || strings.HasPrefix(media, "text/") ||
		strings.Contains(media, "pdf") || strings.Contains(media, "wordprocessingml") ||
		strings.HasPrefix(media, "image/")
}

func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tif", ".tiff", ".webp":
		return true
	default:
		return false
	}
}

func resourceID(kind, spaceID, fileID string) string {
	if kind == resourceKindSpace {
		return resourceKindSpace + resourceSeparator + spaceID
	}
	return resourceKindFile + resourceSeparator + spaceID + resourceSeparator + fileID
}

func parseResourceID(id string) (kind, spaceID, fileID string) {
	parts := strings.SplitN(id, resourceSeparator, 3)
	if len(parts) == 2 && parts[0] == resourceKindSpace {
		return resourceKindSpace, parts[1], ""
	}
	if len(parts) == 3 && parts[0] == resourceKindFile {
		return resourceKindFile, parts[1], parts[2]
	}
	return "", "", ""
}

type dingtalkCursor struct {
	LastSyncTime time.Time         `json:"last_sync_time"`
	FileSignals  map[string]string `json:"file_signals,omitempty"`
}

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
