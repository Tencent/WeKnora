package tools

import (
	"context"
	"io"
	"strings"
	"sync"
	"unicode"
)

// ChatTabularAttachment is a server-authorized CSV or Excel file uploaded in
// chat. It is not a knowledge-base row. The model only ever sees its dN
// handle; Open is the server-side reader for the original bytes.
type ChatTabularAttachment struct {
	ID       string
	FileName string
	FileType string // csv, xlsx, or xls
	Open     func(ctx context.Context) (io.ReadCloser, error)
}

// ChatTabularAttachments is the per-turn allowlist shared by data_schema and
// data_analysis. An ID is readable only after the service inserts it here.
type ChatTabularAttachments struct {
	mu    sync.RWMutex
	items []ChatTabularAttachment
}

// NewChatTabularAttachments returns an empty allowlist.
func NewChatTabularAttachments() *ChatTabularAttachments {
	return &ChatTabularAttachments{}
}

// Set replaces the allowlist. Entries that are not csv/xlsx/xls are dropped
// so a PDF can never be analyzed through this path.
func (a *ChatTabularAttachments) Set(items []ChatTabularAttachment) {
	if a == nil {
		return
	}
	copied := make([]ChatTabularAttachment, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.FileType = TabularFileType(item.FileType)
		if item.ID == "" || item.FileType == "" {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		copied = append(copied, item)
	}
	a.mu.Lock()
	a.items = copied
	a.mu.Unlock()
}

// Get returns the attachment only when this turn's allowlist contains id.
func (a *ChatTabularAttachments) Get(id string) (ChatTabularAttachment, bool) {
	if a == nil {
		return ChatTabularAttachment{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ChatTabularAttachment{}, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, item := range a.items {
		if item.ID == id {
			return item, true
		}
	}
	return ChatTabularAttachment{}, false
}

// List returns a copy of the allowlist for prompt registration.
func (a *ChatTabularAttachments) List() []ChatTabularAttachment {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.items) == 0 {
		return nil
	}
	out := make([]ChatTabularAttachment, len(a.items))
	copy(out, a.items)
	return out
}

// TabularFileType reports csv, xlsx, or xls. A leading dot and case are
// ignored. pdf and every other type return "".
func TabularFileType(fileType string) string {
	ext := strings.ToLower(strings.TrimSpace(fileType))
	ext = strings.TrimPrefix(ext, ".")
	switch ext {
	case "csv", "xlsx", "xls":
		return ext
	default:
		return ""
	}
}

func chatAttachmentTableName(id string) string {
	var b strings.Builder
	b.WriteString("a_")
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}
