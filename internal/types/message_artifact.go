package types

import (
	"time"

	"github.com/google/uuid"
)

// MessageArtifactRecord is one row of message_artifacts: a MessageArtifact
// plus the message it belongs to and its position in that message's list.
// Position is the index the download endpoint addresses the file by.
type MessageArtifactRecord struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	SessionID   string `gorm:"column:session_id"`
	MessageID   string `gorm:"column:message_id"`
	Position    int    `gorm:"column:position"`
	URL         string `gorm:"column:url"`
	FileName    string `gorm:"column:file_name"`
	FileType    string `gorm:"column:file_type"`
	FileSize    int64  `gorm:"column:file_size"`
	ContentHash string `gorm:"column:content_hash"`
	SourcePath  string `gorm:"column:source_path"`
	// ModTime is RFC 3339 text rather than a timestamp column: the collector
	// matches files on (source path, mtime) at nanosecond precision, which
	// PostgreSQL timestamps would truncate to microseconds. Empty means zero.
	ModTime   string    `gorm:"column:mod_time"`
	CreatedAt time.Time `gorm:"column:created_at"`
	// DeletedAt is set when the user deleted the file. The row is a tombstone
	// from then on: it holds position (the download address) and keeps the
	// collector from re-attaching the sandbox file, while the blob itself has
	// been reclaimed. URL is left in place so a later GC can retry a reclaim
	// that failed.
	DeletedAt *time.Time `gorm:"column:deleted_at"`
}

// TableName implements gorm's tabler.
func (MessageArtifactRecord) TableName() string { return "message_artifacts" }

// NewMessageArtifactRecords turns a message's artifact list into table rows.
//
// CreatedAt is stored in UTC: SQLite compares DATETIME values as text, so
// mixing zones would break ORDER BY created_at. A zero CreatedAt (an artifact
// built outside the collector) falls back to now, matching what the collector
// would have stamped.
func NewMessageArtifactRecords(sessionID, messageID string, list MessageArtifacts) []MessageArtifactRecord {
	if len(list) == 0 {
		return nil
	}
	now := time.Now().UTC()
	rows := make([]MessageArtifactRecord, 0, len(list))
	for i, a := range list {
		created := a.CreatedAt.UTC()
		if a.CreatedAt.IsZero() {
			created = now
		}
		rows = append(rows, MessageArtifactRecord{
			ID:          uuid.New().String(),
			SessionID:   sessionID,
			MessageID:   messageID,
			Position:    i,
			URL:         a.URL,
			FileName:    a.FileName,
			FileType:    a.FileType,
			FileSize:    a.FileSize,
			ContentHash: a.ContentHash,
			SourcePath:  a.SourcePath,
			ModTime:     FormatArtifactModTime(a.ModTime),
			CreatedAt:   created,
			DeletedAt:   a.DeletedAt,
		})
	}
	return rows
}

// FormatArtifactModTime encodes an mtime for message_artifacts.mod_time.
func FormatArtifactModTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

// ParseArtifactModTime decodes message_artifacts.mod_time; anything
// unparseable reads as the zero time, which only costs the collector a hash.
func ParseArtifactModTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Artifact converts the row back to the value carried on Message.Artifacts.
func (r MessageArtifactRecord) Artifact() MessageArtifact {
	return MessageArtifact{
		URL:         r.URL,
		FileName:    r.FileName,
		FileType:    r.FileType,
		FileSize:    r.FileSize,
		ContentHash: r.ContentHash,
		SourcePath:  r.SourcePath,
		ModTime:     ParseArtifactModTime(r.ModTime),
		CreatedAt:   r.CreatedAt,
		DeletedAt:   r.DeletedAt,
	}
}

// ArtifactLibraryQuery lists the artifacts across every session the caller can
// see. TenantID and UserID are filled by the service from the request context.
type ArtifactLibraryQuery struct {
	TenantID uint64
	UserID   string
	// Keyword matches the file name, case-insensitively.
	Keyword string
	// FileTypes restricts results to these extensions (".pdf", ".pptx", ...).
	FileTypes []string
	Page      int
	PageSize  int
}

// ArtifactLibraryItem is the latest version of one file in one session.
// Versions are the artifacts that share a session and sandbox source path:
// regenerating report.pptx three times is one item with VersionCount 3.
type ArtifactLibraryItem struct {
	SessionID    string    `json:"session_id"    gorm:"column:session_id"`
	SessionTitle string    `json:"session_title" gorm:"column:session_title"`
	MessageID    string    `json:"message_id"    gorm:"column:message_id"`
	Index        int       `json:"index"         gorm:"column:position"`
	URL          string    `json:"-"             gorm:"column:url"`
	Handle       string    `json:"handle,omitempty" gorm:"-"`
	FileName     string    `json:"file_name"     gorm:"column:file_name"`
	FileType     string    `json:"file_type"     gorm:"column:file_type"`
	FileSize     int64     `json:"file_size"     gorm:"column:file_size"`
	SourcePath   string    `json:"source_path"   gorm:"column:source_path"`
	CreatedAt    time.Time `json:"created_at"    gorm:"column:created_at"`
	VersionCount int       `json:"version_count" gorm:"column:version_count"`
}

// ArtifactRef addresses one artifact row by its business key. The primary key
// is not usable for this: writeMessageArtifacts rewrites a message's rows with
// fresh UUIDs whenever the message is updated, while (message, position) is
// stable for the life of the artifact.
type ArtifactRef struct {
	MessageID string
	Position  int
}

// ArtifactDeleteRequest addresses the artifact a user asked to delete.
// SessionID/MessageID/Index are the same coordinates the download endpoint
// takes, so a client deletes exactly what it was showing.
type ArtifactDeleteRequest struct {
	SessionID string
	MessageID string
	Index     int
	// AllVersions extends the delete to every regeneration of the same sandbox
	// file in this session. The artifact library shows one row per file with a
	// version count, so deleting from there sets this; the in-chat panel lists
	// each version separately and leaves it false.
	AllVersions bool
}

// ArtifactBlobRef is one stored object the delete orphaned. The caller resolves
// the owning tenant's file service and releases the resource binding before
// removing the bytes, so a blob another message or knowledge entry still claims
// survives.
//
// MessageIDs is every deleted message that owned this object: a later answer
// that re-attaches an earlier file stores its own row and its own binding, so
// one delete can retire several claims on the same bytes and all of them have
// to be released before the object counts as unreferenced.
type ArtifactBlobRef struct {
	URL        string
	MessageIDs []string
}

// ArtifactDeleteResult reports what a delete tombstoned.
type ArtifactDeleteResult struct {
	// FileName of the artifact the request addressed, for the response body.
	FileName string
	// Deleted counts the rows this call tombstoned. Zero means another caller
	// got there first; the blobs are then theirs to reclaim, not ours.
	Deleted int
	// Reclaim lists the distinct blobs whose bytes may now be removed.
	Reclaim []ArtifactBlobRef
}
