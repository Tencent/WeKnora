package interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ErrAttachmentNotFound indicates that a reference is absent from the authorized inventory.
var ErrAttachmentNotFound = errors.New("attachment is not in this conversation")

// AttachmentReader is the application boundary used by attachment:// tools.
// Implementations authorize against the session inventory before opening content.
type AttachmentReader interface {
	List(context.Context) ([]AttachmentDocument, error)
	Resolve(ctx context.Context, ref string, wait bool) (*AttachmentDocument, error)
	ReadImage(ctx context.Context, documentID string, number int, detail, focus string) (*AttachmentImage, error)
	WaitTimeout() time.Duration
}

// AttachmentDocument contains a durable result and any successfully materialized text path.
type AttachmentDocument struct {
	Attachment types.MessageAttachment
	Document   *types.TemporaryDocument
	ParsedPath string
	Warning    string
}

// AttachmentImage is prepared visual evidence with an optional scoped observation.
type AttachmentImage struct {
	DataURI string
	// Non-nil means description was attempted; an empty value must not trigger
	// another VLM call in the engine. Native vision keeps this nil.
	Description *string
	Files       map[string]string
	Warning     string
}
