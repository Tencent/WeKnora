package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// WikiLogPageRef identifies a wiki page that was affected by a log event,
// paired with the human-readable title at the time of the event. Title is
// captured at write time so the log feed stays readable even if the page
// is later renamed or deleted — the previously-stored title is the one
// the user remembers seeing in the event stream.
type WikiLogPageRef struct {
	Slug  string `json:"slug"`
	Title string `json:"title,omitempty"`
}

// WikiLogPageRefs is a JSON-marshalled list of page references. The
// column is TEXT/JSONB depending on driver; the Value/Scan pair keeps
// GORM happy on both Postgres and the SQLite tests.
type WikiLogPageRefs []WikiLogPageRef

// Value implements the driver.Valuer interface.
func (r WikiLogPageRefs) Value() (driver.Value, error) {
	if r == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(r)
}

// Scan implements the sql.Scanner interface. Also tolerates the legacy
// []string shape so we can roll forward without a data migration — any
// rows the ingest pipeline wrote before this commit surface as refs
// with a slug and an empty title, which the frontend renders identically.
func (r *WikiLogPageRefs) Scan(value interface{}) error {
	if value == nil {
		*r = nil
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("unsupported type for WikiLogPageRefs: %T", value)
	}

	// Try the new [{slug,title}] shape first.
	var refs []WikiLogPageRef
	if err := json.Unmarshal(raw, &refs); err == nil {
		*r = refs
		return nil
	}
	// Fall back to the legacy []string shape.
	var slugs []string
	if err := json.Unmarshal(raw, &slugs); err != nil {
		return err
	}
	out := make([]WikiLogPageRef, 0, len(slugs))
	for _, s := range slugs {
		out = append(out, WikiLogPageRef{Slug: s})
	}
	*r = out
	return nil
}

// WikiLogEntry is a single ingest/retract/edit event appended to the
// per-KB operation log. It replaces the legacy "single giant TEXT column
// on slug='log' wiki_pages row" model, which rewrote the whole column on
// every event and caused O(n^2) write amplification as the KB grew.
//
// Each event is one INSERT; reads paginate by (knowledge_base_id, id DESC).
type WikiLogEntry struct {
	// Auto-increment identifier. Monotonic within a single database, so
	// frontend pagination uses it as a stable cursor without needing to
	// disambiguate identical created_at values.
	ID uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	// Tenant scope, mirrored from the enclosing knowledge base.
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// Knowledge base this event belongs to.
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	// Short operation tag: "ingest", "retract", etc. Matches the `action`
	// argument historically passed to appendLogEntry.
	Action string `json:"action" gorm:"type:varchar(32)"`
	// Knowledge ID the event was about (may be empty for KB-level events).
	KnowledgeID string `json:"knowledge_id" gorm:"type:varchar(36);default:''"`
	// Document title at the time of the event. Stored verbatim rather than
	// joined at read time so deleted knowledge still has a human-readable
	// label in the log.
	DocTitle string `json:"doc_title" gorm:"type:text"`
	// One-line summary of the change, as it was when the event was logged.
	Summary string `json:"summary" gorm:"type:text"`
	// Wiki pages affected by this event. Each ref carries both slug (for
	// navigation) and title (for display) so the log renders human-
	// readable text without a post-hoc slug→title lookup that might fail
	// for now-deleted pages.
	PagesAffected WikiLogPageRefs `json:"pages_affected" gorm:"type:jsonb;default:'[]'"`
	// Server-side timestamp (UTC).
	CreatedAt time.Time `json:"created_at"`
}

// TableName specifies the database table name.
func (WikiLogEntry) TableName() string {
	return "wiki_log_entries"
}

// WikiLogEntryListResponse is the paginated response for GET /wiki/log.
// NextCursor is the stringified ID of the oldest entry in `Entries`; pass
// it back as `cursor` to fetch the next page. Empty string means "no more".
type WikiLogEntryListResponse struct {
	Entries    []*WikiLogEntry `json:"entries"`
	NextCursor string          `json:"next_cursor,omitempty"`
}
