// Package notion implements the Notion data source connector for WeKnora.
//
// It syncs pages, databases, and attachments from Notion workspaces into WeKnora knowledge bases.
//
// Notion API docs:
//   - Authentication: https://developers.notion.com/docs/authorization
//   - Search:         https://developers.notion.com/reference/post-search
//   - Pages:          https://developers.notion.com/reference/retrieve-a-page
//   - Blocks:         https://developers.notion.com/reference/retrieve-a-block
//   - Databases:      https://developers.notion.com/reference/retrieve-a-database
package notion

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// NotionAPIVersion is the Notion API version used by this connector.
const NotionAPIVersion = "2026-03-11"

// DefaultBaseURL is the Notion API base URL.
const DefaultBaseURL = "https://api.notion.com"

// Config holds Notion-specific configuration for the data source connector.
type Config struct {
	APIKey string `json:"api_key"` // Internal Integration Token
}

// parseNotionConfig extracts and validates Notion config from DataSourceConfig.
func parseNotionConfig(config *types.DataSourceConfig) (*Config, error) {
	if config == nil {
		return nil, datasource.ErrInvalidConfig
	}

	tokenVal, ok := config.Credentials["api_key"]
	if !ok {
		return nil, fmt.Errorf("%w: missing api_key", datasource.ErrInvalidCredentials)
	}
	token, ok := tokenVal.(string)
	if !ok || token == "" {
		return nil, fmt.Errorf("%w: api_key must be a non-empty string", datasource.ErrInvalidCredentials)
	}

	return &Config{APIKey: token}, nil
}

// --- API response types ---

// notionPage represents a Notion page or database object.
type notionPage struct {
	ID             string       `json:"id"`
	Object         string       `json:"object"` // "page" | "database" | "data_source" (2025-09-03+)
	Parent         notionParent `json:"parent"`
	URL            string       `json:"url"`
	LastEditedTime time.Time    `json:"last_edited_time"`
	InTrash        bool         `json:"in_trash"`
	// Title is extracted from properties after unmarshaling; not directly from JSON.
	Title string `json:"-"`
	// RawTitle holds the top-level title array (used by database objects).
	RawTitle json.RawMessage `json:"title,omitempty"`
	// RawProperties holds the raw JSON for property extraction.
	RawProperties json.RawMessage `json:"properties"`
	// DatabaseParent indicates where the database lives in the workspace hierarchy.
	// Only present on data_source objects (API 2025-09-03+). For example, if a database
	// is inside a page, this will be {type: "page_id", page_id: "..."}.
	DatabaseParent *notionParent `json:"database_parent,omitempty"`
}

// Parent type constants for notionParent.Type
const (
	parentTypeWorkspace    = "workspace"
	parentTypePageID       = "page_id"
	parentTypeDatabaseID   = "database_id"
	parentTypeDataSourceID = "data_source_id"
	parentTypeBlockID      = "block_id"
)

// notionParent represents the parent relationship of a page/database.
type notionParent struct {
	Type         string `json:"type"`
	PageID       string `json:"page_id,omitempty"`
	DatabaseID   string `json:"database_id,omitempty"`
	DataSourceID string `json:"data_source_id,omitempty"`
	BlockID      string `json:"block_id,omitempty"`
}

// GetParentID returns the effective parent ID regardless of parent type.
func (p *notionParent) GetParentID() string {
	switch p.Type {
	case parentTypePageID:
		return p.PageID
	case parentTypeDatabaseID:
		return p.DatabaseID
	case parentTypeDataSourceID:
		return p.DataSourceID
	case parentTypeBlockID:
		return p.BlockID
	default:
		return ""
	}
}

// isDatabase returns true if the notionPage represents a database or data source.
func (p *notionPage) isDatabase() bool {
	return p.Object == "database" || p.Object == "data_source"
}

// notionBlock represents a single content block in a Notion page.
// Requires custom UnmarshalJSON to extract the type-specific content.
type notionBlock struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	HasChildren bool            `json:"has_children"`
	RawContent  json.RawMessage `json:"-"` // Extracted from the type-named field (e.g., "paragraph")
	Children    []notionBlock   `json:"-"` // Populated by client.GetBlockChildrenAll, not from API directly
}

// UnmarshalJSON implements custom JSON unmarshaling for notionBlock.
// It extracts the dynamic type-keyed field (e.g., {"type":"paragraph","paragraph":{...}})
// into RawContent.
func (b *notionBlock) UnmarshalJSON(data []byte) error {
	// Use a helper struct to avoid infinite recursion
	type blockAlias struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		HasChildren bool   `json:"has_children"`
	}

	var alias blockAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	b.ID = alias.ID
	b.Type = alias.Type
	b.HasChildren = alias.HasChildren

	// Extract the type-named field into RawContent
	if alias.Type != "" {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		if content, ok := raw[alias.Type]; ok {
			b.RawContent = content
		}
	}

	return nil
}

// notionRichText represents a rich text element in Notion.
type notionRichText struct {
	Type        string             `json:"type"` // "text" | "mention" | "equation"
	PlainText   string             `json:"plain_text"`
	Href        string             `json:"href,omitempty"`
	Annotations notionAnnotations  `json:"annotations"`
	Text        *notionTextContent `json:"text,omitempty"`
	Mention     *notionMention     `json:"mention,omitempty"`
	Equation    *notionEquation    `json:"equation,omitempty"`
}

// notionAnnotations holds text style information.
type notionAnnotations struct {
	Bold          bool   `json:"bold"`
	Italic        bool   `json:"italic"`
	Strikethrough bool   `json:"strikethrough"`
	Underline     bool   `json:"underline"`
	Code          bool   `json:"code"`
	Color         string `json:"color"`
}

// notionTextContent holds the content for "text" type rich text.
type notionTextContent struct {
	Content string `json:"content"`
	Link    *struct {
		URL string `json:"url"`
	} `json:"link,omitempty"`
}

// notionMention holds the content for "mention" type rich text.
type notionMention struct {
	Type string `json:"type"` // "page" | "database" | "date" | "user" | "link_preview"
	Page *struct {
		ID string `json:"id"`
	} `json:"page,omitempty"`
	Database *struct {
		ID string `json:"id"`
	} `json:"database,omitempty"`
	Date        *notionDateMention `json:"date,omitempty"`
	LinkPreview *struct {
		URL string `json:"url"`
	} `json:"link_preview,omitempty"`
}

// notionDateMention holds date information for a mention.
type notionDateMention struct {
	Start string `json:"start"`
	End   string `json:"end,omitempty"`
}

// notionEquation holds the content for "equation" type rich text.
type notionEquation struct {
	Expression string `json:"expression"`
}

// notionFile represents a file object in Notion (used by image, file, pdf blocks).
type notionFile struct {
	Type string `json:"type"` // "file" | "external" | "file_upload"
	File *struct {
		URL        string    `json:"url"`
		ExpiryTime time.Time `json:"expiry_time"`
	} `json:"file,omitempty"`
	External *struct {
		URL string `json:"url"`
	} `json:"external,omitempty"`
	FileUpload *struct {
		ID string `json:"id"`
	} `json:"file_upload,omitempty"`
	Caption []notionRichText `json:"caption,omitempty"`
	Name    string           `json:"name,omitempty"`
}

// GetURL returns the file URL regardless of type (hosted or external).
// For file_upload type, returns empty — caller must resolve via client.GetFileUploadURL().
func (f *notionFile) GetURL() string {
	if f.File != nil {
		return f.File.URL
	}
	if f.External != nil {
		return f.External.URL
	}
	return ""
}

// GetFileUploadID returns the file upload ID for file_upload type, empty otherwise.
func (f *notionFile) GetFileUploadID() string {
	if f.FileUpload != nil {
		return f.FileUpload.ID
	}
	return ""
}

// --- Cursor ---

// notionCursor tracks state for incremental sync.
// Only PageEditTimes is used for diffing; LastSyncTime lives on types.SyncCursor.
type notionCursor struct {
	PageEditTimes map[string]time.Time `json:"page_edit_times"` // page_id → last_edited_time
}

// --- Attachment ---

// attachment represents a file to be downloaded from a Notion page.
type attachment struct {
	URL      string // Notion S3 signed URL (expires in 1 hour)
	FileName string
	Type     string // "image" | "file" | "pdf" | "video" | "audio"
}

// --- Pagination ---

// paginatedResponse is the common response wrapper for paginated Notion API responses.
type paginatedResponse struct {
	Object     string          `json:"object"` // "list"
	Results    json.RawMessage `json:"results"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

