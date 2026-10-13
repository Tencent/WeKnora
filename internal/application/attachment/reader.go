// Package attachment owns session-authorized attachment reads. It has no agent,
// tool, database or sandbox dependency; adapters supply storage and projections.
package attachment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const pollInterval = 500 * time.Millisecond

// Store supplies the session inventory and tenant-scoped document lookup.
type Store interface {
	SessionAttachments(context.Context) (types.MessageAttachments, error)
	TemporaryDocument(context.Context, string) (*types.TemporaryDocument, error)
}

// Reader coordinates authorized attachment reads independently of tools and sandboxes.
type Reader struct {
	store      Store
	parseWait  time.Duration
	readImage  func(context.Context, string) ([]byte, error)
	Images     *ImageReader
	Projection Projection
}

// NewReader binds durable storage to one conversation and its parse wait budget.
func NewReader(store Store, wait time.Duration, readImage func(context.Context, string) ([]byte, error)) *Reader {
	return &Reader{store: store, parseWait: wait, readImage: readImage}
}

// WaitTimeout bounds parsing plus one image batch: the batch is described
// concurrently, with room for one queue round behind other reads.
func (r *Reader) WaitTimeout() time.Duration {
	wait := r.parseWait
	if r.Images != nil && r.Images.Describe != nil {
		wait += imageDescribeFullTimeout + imageDescribeTimeout
	}
	return wait
}

// List loads the authorized inventory once, then resolves each document without
// waiting or projecting files. Listing must not reload the message history per item.
func (r *Reader) List(ctx context.Context) ([]interfaces.AttachmentDocument, error) {
	inventory, err := r.inventory(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]interfaces.AttachmentDocument, 0, len(inventory))
	for _, item := range inventory {
		doc, err := r.store.TemporaryDocument(ctx, item.ID)
		if err != nil {
			return nil, fmt.Errorf("load attachment %s: %w", item.ID, err)
		}
		out = append(out, interfaces.AttachmentDocument{Attachment: item, Document: doc})
	}
	return out, nil
}

// Resolve opens a session attachment, optionally waiting for parsing and syncing its text.
func (r *Reader) Resolve(ctx context.Context, ref string, wait bool) (*interfaces.AttachmentDocument, error) {
	inventory, err := r.inventory(ctx)
	if err != nil {
		return nil, err
	}
	item, err := findAttachment(inventory, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", interfaces.ErrAttachmentNotFound, err)
	}
	var doc *types.TemporaryDocument
	if wait {
		doc, err = r.awaitDocument(ctx, item.ID)
	} else {
		doc, err = r.store.TemporaryDocument(ctx, item.ID)
	}
	if err != nil {
		return nil, err
	}
	result := &interfaces.AttachmentDocument{Attachment: item, Document: doc}
	if wait && doc != nil && doc.Status == types.TemporaryDocumentStatusReady && r.Projection != nil {
		result.ParsedPath, err = r.Projection.Document(ctx, doc)
		if err != nil {
			result.Warning = "Could not sync parsed text to sandbox: " + err.Error()
		}
	}
	return result, nil
}

// inventory returns the session's stored attachments, deduplicated by ID.
// Inline uploads without a stored document have nothing to read and are left
// out.
func (r *Reader) inventory(ctx context.Context) (types.MessageAttachments, error) {
	if r.store == nil {
		return nil, fmt.Errorf("attachments are unavailable")
	}
	all, err := r.store.SessionAttachments(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load conversation attachments: %w", err)
	}
	seen := make(map[string]int, len(all))
	out := make(types.MessageAttachments, 0, len(all))
	for _, attachment := range all {
		id := strings.TrimSpace(attachment.ID)
		if id == "" {
			continue
		}
		if i, ok := seen[id]; ok {
			// Later rows carry the content persisted after resolution.
			if out[i].Content == "" {
				out[i] = attachment
			}
			continue
		}
		seen[id] = len(out)
		out = append(out, attachment)
	}
	return out, nil
}

func findAttachment(inventory types.MessageAttachments, ref string) (types.MessageAttachment, error) {
	ref = strings.TrimSpace(ref)
	var byName []types.MessageAttachment
	for _, attachment := range inventory {
		if types.AttachmentHandle(attachment.ID) == ref || attachment.ID == ref {
			return attachment, nil
		}
		if strings.EqualFold(attachment.FileName, ref) {
			byName = append(byName, attachment)
		}
	}
	if len(byName) > 0 {
		return byName[len(byName)-1], nil
	}
	paths := make([]string, 0, len(inventory))
	for _, attachment := range inventory {
		paths = append(paths, fmt.Sprintf("%s (%s)", types.AttachmentPath(attachment.ID), attachment.FileName))
	}
	if len(paths) == 0 {
		return types.MessageAttachment{}, fmt.Errorf("this conversation has no readable attachments")
	}
	return types.MessageAttachment{}, fmt.Errorf(
		"unknown attachment %q; available: %s", ref, strings.Join(paths, ", "))
}

// awaitDocument loads the document, polling while it is still being parsed
// until parseWait elapses or ctx is cancelled.
func (r *Reader) awaitDocument(ctx context.Context, id string) (*types.TemporaryDocument, error) {
	deadline := time.Now().Add(r.parseWait)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		document, err := r.store.TemporaryDocument(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("failed to load attachment: %w", err)
		}
		if document == nil || !attachmentPending(document.Status) || !time.Now().Before(deadline) {
			return document, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func attachmentPending(status string) bool {
	return status == types.TemporaryDocumentStatusUploaded || status == types.TemporaryDocumentStatusProcessing
}
