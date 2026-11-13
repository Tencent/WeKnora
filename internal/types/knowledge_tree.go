package types

import (
	"fmt"
	"strconv"
)

// MaxKnowledgeDepth bounds a native knowledge parent chain.
const MaxKnowledgeDepth = 32

// MaxKnowledgeChildren bounds the entries expanded or deleted in one request.
const MaxKnowledgeChildren = 1000

// KnowledgeChildrenOptions controls descendant expansion depth and response size.
type KnowledgeChildrenOptions struct{ Depth, Limit int }

// MaxKnowledgeTreePassages caps additional billed passages independently of the knowledge limit.
const MaxKnowledgeTreePassages = 1000

// KnowledgeTreeDiagnostics describes the bounded pre-rerank expansion, not vector recall success.
type KnowledgeTreeDiagnostics struct {
	Applied        bool `json:"applied"`
	AddedKnowledge int  `json:"added_knowledge"`
	AddedPassages  int  `json:"added_passages"`
	Truncated      bool `json:"truncated"`
}

// KnowledgeTreePassage carries actual stored evidence and its structural provenance.
type KnowledgeTreePassage struct {
	Knowledge         *Knowledge
	Chunk             *Chunk
	AnchorKnowledgeID string
	Depth             int
}

// ParseKnowledgeChildrenOptions validates the HTTP depth and limit parameters.
func ParseKnowledgeChildrenOptions(depth, limit string) (KnowledgeChildrenOptions, error) {
	o := KnowledgeChildrenOptions{Limit: 200}
	var err error
	if depth == "all" {
		o.Depth = -1
	} else if depth != "" {
		o.Depth, err = strconv.Atoi(depth)
		if err != nil {
			return o, fmt.Errorf("invalid children_depth")
		}
	}
	if limit != "" {
		o.Limit, err = strconv.Atoi(limit)
		if err != nil {
			return o, fmt.Errorf("invalid children_limit")
		}
	}
	if o.Depth < -1 || o.Depth > MaxKnowledgeDepth || o.Limit < 1 || o.Limit > MaxKnowledgeChildren {
		return o, fmt.Errorf("children_depth must be -1/all or 0-32; children_limit must be 1-1000")
	}
	return o, nil
}

// KnowledgeTreeRoot identifies a root from an already authorized read or search.
type KnowledgeTreeRoot struct {
	TenantID          uint64
	KBID, KnowledgeID string
}

// KnowledgeChild is supplemental source content, not a scored vector hit.
type KnowledgeChild struct {
	KnowledgeID       string `json:"knowledge_id"                gorm:"column:id"`
	ParentKnowledgeID string `json:"parent_knowledge_id"`
	AnchorKnowledgeID string `json:"anchor_knowledge_id"         gorm:"-"`
	Depth             int    `json:"depth"                       gorm:"-"`
	Title             string `json:"title"`
	Type              string `json:"type"`
	ParseStatus       string `json:"parse_status"`
	EnableStatus      string `json:"enable_status"`
	Content           string `json:"content,omitempty"           gorm:"-"`
	ContentAvailable  bool   `json:"content_available"           gorm:"-"`
	ContentTruncated  bool   `json:"content_truncated,omitempty" gorm:"-"`
	ContentURL        string `json:"content_url"                 gorm:"-"`
	AlreadyHit        bool   `json:"already_hit,omitempty"       gorm:"-"`
	Metadata          string `json:"-"`
	TenantID          uint64 `json:"-"`
	KnowledgeBaseID   string `json:"-"`
}
