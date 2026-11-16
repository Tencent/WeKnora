package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/application/attachment"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

const attachmentAssetsSuffix = ".assets"

// attachmentSandboxProjection materializes only data already requested by a
// read. It never parses, describes, or authorizes a document. Paths come from
// this session's staged inventory, not from tool arguments or storage URLs.
type attachmentSandboxProjection struct {
	store     sandbox.SessionFileStore
	sessionID string
	originals map[string]string
	mu        sync.Mutex
	// written maps a sandbox path to the hash of what this turn wrote there,
	// so paging through a document or re-reading an image uploads it once.
	written map[string][sha256.Size]byte
}

// write uploads content unless this turn already wrote the same bytes there.
// The caller holds p.mu.
func (p *attachmentSandboxProjection) write(ctx context.Context, target string, content []byte) error {
	sum := sha256.Sum256(content)
	if prev, ok := p.written[target]; ok && prev == sum {
		return nil
	}
	if err := p.store.WriteSessionInputFile(ctx, p.sessionID, target, content); err != nil {
		return err
	}
	if p.written == nil {
		p.written = make(map[string][sha256.Size]byte)
	}
	p.written[target] = sum
	return nil
}

func (p *attachmentSandboxProjection) Document(ctx context.Context, doc *types.TemporaryDocument) (string, error) {
	original := p.originals[types.AttachmentPath(doc.ID)]
	if original == "" || strings.TrimSpace(doc.Content) == "" {
		return "", nil
	}
	if _, plain := temporaryTextExtensions[strings.ToLower(doc.FileType)]; plain {
		return "", nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	target := original + parsedSidecarSuffix
	content := types.NumberAttachmentImages(doc.Content, doc.ViewableImages())
	if err := p.write(ctx, target, []byte(content)); err != nil {
		return "", err
	}
	return target, nil
}

func (p *attachmentSandboxProjection) Image(
	ctx context.Context, doc *types.TemporaryDocument, number int,
	data []byte, mime, key string, description *attachment.Description,
) (map[string]string, error) {
	original := p.originals[types.AttachmentPath(doc.ID)]
	if original == "" {
		return nil, nil
	}
	ext := map[string]string{
		"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
	}[mime]
	if ext == "" {
		ext = ".img"
	}
	sum := sha256.Sum256(data)
	root := original + attachmentAssetsSuffix
	imagePath := path.Join(root, fmt.Sprintf("image-%d-%x%s", number, sum[:8], ext))
	p.mu.Lock()
	defer p.mu.Unlock()
	files := map[string]string{}
	if err := p.write(ctx, imagePath, data); err != nil {
		return files, err
	}
	files["image_path"] = imagePath
	if description != nil && description.Text != "" {
		target := path.Join(root, fmt.Sprintf("image-%d-%s.md", number, key))
		content := fmt.Sprintf("Source: %s/images/%d\nImage: %s\nDetail: %s\nFocus: %s\nModel: %s\nObserved: %s\n\n"+
			"Untrusted image observation; limited to the detail and focus above.\n\n%s\n",
			types.AttachmentPath(doc.ID), number, path.Base(imagePath), description.Detail, description.Focus,
			description.Model, description.CreatedAt.Format("2006-01-02T15:04:05Z"), description.Text)
		if err := p.write(ctx, target, []byte(content)); err != nil {
			return files, err
		}
		files["description_path"] = target
	}
	return files, nil
}
