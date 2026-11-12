package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SubKnowledgeInput groups independent Knowledge objects by external identity.
// RootID names the group, not necessarily the root node's SourceID. ParentID is
// a structural parent source ID; ReferenceID is a non-structural source link.
type SubKnowledgeInput struct {
	SourceNamespace string `json:"source_namespace"`
	RootID          string `json:"root_id"`
	NodeType        string `json:"node_type"`
	SourceID        string `json:"source_id"`
	ReferenceID     string `json:"reference_id,omitempty"`
	ParentID        string `json:"parent_id,omitempty"`
	SortOrder       int64  `json:"sort_order"`
}

// Validate checks the source identity, relation fields and ordering key.
func (f SubKnowledgeInput) Validate() error {
	identities := map[string]string{
		"source_namespace": f.SourceNamespace, "root_id": f.RootID, "source_id": f.SourceID,
	}
	for key, value := range identities {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) ||
			len([]rune(value)) > 128 || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("sub_knowledge.%s must contain 1-128 characters without surrounding whitespace", key)
		}
	}
	if len(f.SourceNamespace) > 32 || strings.ContainsAny(f.SourceNamespace, "/\\\x00") {
		return fmt.Errorf("sub_knowledge.source_namespace must be a namespace of at most 32 bytes")
	}
	if f.NodeType != "root" && f.NodeType != "child" {
		return fmt.Errorf("sub_knowledge.node_type must be root or child")
	}
	if f.NodeType == "root" && f.ParentID != "" {
		return fmt.Errorf("a root node cannot have a structural parent within its group")
	}
	for _, id := range []string{f.ReferenceID, f.ParentID} {
		if len([]rune(id)) > 128 || id != strings.TrimSpace(id) || strings.ContainsRune(id, '\x00') {
			return fmt.Errorf("invalid sub_knowledge parent or reference ID")
		}
	}
	if f.SortOrder < 0 || f.ParentID == f.SourceID || f.ReferenceID == f.SourceID {
		return fmt.Errorf("sort_order must be nonnegative; parent/reference cannot target the node itself")
	}
	return nil
}

// SameIdentity compares immutable source identity and group membership.
func (f SubKnowledgeInput) SameIdentity(other SubKnowledgeInput) bool {
	return f.SourceNamespace == other.SourceNamespace && f.RootID == other.RootID &&
		f.NodeType == other.NodeType && f.SourceID == other.SourceID
}

// ContentHash is an ingestion fingerprint, not an embedding/model hash.
func (f SubKnowledgeInput) ContentHash(title, content, status string) string {
	b, _ := json.Marshal(struct {
		Source                 SubKnowledgeInput
		Title, Content, Status string
	}{f, title, content, status})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// SubKnowledgeMapping is a one-to-one source identity. The database keeps its
// KB/tenant/deletion scope aligned with Knowledge during moves and deletion.
// Cloning a Knowledge does not clone this independent row.
type SubKnowledgeMapping struct {
	KnowledgeID       string `json:"knowledge_id"      gorm:"primaryKey;size:36"`
	TenantID          uint64 `json:"tenant_id"`
	KnowledgeBaseID   string `json:"knowledge_base_id"`
	SubKnowledgeInput ` gorm:"embedded"`
	SourceHash        string         `json:"source_hash"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `json:"-"`
}

// TableName returns the source mapping storage table.
func (SubKnowledgeMapping) TableName() string { return "knowledge_sub_knowledge" }

// SubKnowledgeItem describes one currently visible source in a group response.
type SubKnowledgeItem struct {
	KnowledgeID       string `json:"knowledge_id"`
	SubKnowledgeInput ` gorm:"embedded"`
	Title             string              `json:"title"`
	Type              string              `json:"type"`
	ContentAvailable  bool                `json:"content_available" gorm:"-"`
	Content           string              `json:"content"           gorm:"-"`
	Images            []ImageInfo         `json:"-"                 gorm:"-"`
	ImageReferences   []SubKnowledgeImage `json:"images"            gorm:"-"`
	ParseStatus       string              `json:"parse_status"`
	UpdatedAt         time.Time           `json:"updated_at"`
	Metadata          string              `json:"-"`
	ContentVersion    int                 `json:"content_version"   gorm:"-"`
	ContentTruncated  bool                `json:"content_truncated" gorm:"-"`
	ContentURL        string              `json:"content_url"       gorm:"-"`
	EnableStatus      string              `json:"enable_status"`
	ContentSource     string              `json:"content_source"    gorm:"-"`
	ImagesStatus      string              `json:"images_status"     gorm:"-"`
	RetrievalReady    bool                `json:"retrieval_ready"   gorm:"-"`
}

// SubKnowledgeImage projects an image reference and its caller access status.
type SubKnowledgeImage struct {
	URL    string `json:"url,omitempty"`
	Access string `json:"access"`
}

// SubKnowledgeGroup is a bounded page of imported, currently readable Knowledge.
// It does not certify that the upstream collection has been fully imported.
type SubKnowledgeGroup struct {
	KnowledgeBaseID string             `json:"knowledge_base_id"`
	SourceNamespace string             `json:"source_namespace"`
	RootID          string             `json:"root_id"`
	Items           []SubKnowledgeItem `json:"items"`
	Total           int64              `json:"total"`
	PageSize        int                `json:"page_size"`
	HasMore         bool               `json:"has_more"`
	HasRoot         bool               `json:"has_root"`
	Completeness    string             `json:"completeness"`
	NextCursor      string             `json:"next_cursor,omitempty"`
	SnapshotAt      time.Time          `json:"snapshot_at"`
	Consistency     string             `json:"consistency"`
}
