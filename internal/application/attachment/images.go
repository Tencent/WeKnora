package attachment

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/models/imageprep"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"golang.org/x/sync/singleflight"
)

const imageDescribeTimeout = 30 * time.Second

// Description records the scope of an observation; it is never merged into
// parsed document text or treated as a complete reading of the image.
type Description struct {
	Text      string    `json:"text"`
	Detail    string    `json:"detail"`
	Focus     string    `json:"focus,omitempty"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
}

// DescriptionCache persists scoped observations alongside their owning document.
type DescriptionCache interface {
	Get(ctx context.Context, documentID, key string) (*Description, error)
	Put(ctx context.Context, documentID, key string, value Description) error
}

// Projection is optional. The durable document and cache remain usable when
// the sandbox is absent, expires, or fails to accept a copy.
type Projection interface {
	Document(context.Context, *types.TemporaryDocument) (string, error)
	Image(
		ctx context.Context, doc *types.TemporaryDocument, number int,
		data []byte, mime, key string, description *Description,
	) (map[string]string, error)
}

// ImageReader describes requested images and reuses equivalent prior observations.
type ImageReader struct {
	Model     string
	Describe  func(context.Context, []byte, string) (string, error)
	Cache     DescriptionCache
	calls     singleflight.Group
	slotsOnce sync.Once
	slots     chan struct{}
}

// ReadImage authorizes, prepares and optionally describes and projects one image.
func (r *Reader) ReadImage(
	ctx context.Context, documentID string, number int, detail, focus string,
) (*interfaces.AttachmentImage, error) {
	resolved, err := r.Resolve(ctx, documentID, false)
	if err != nil {
		return nil, err
	}
	doc := resolved.Document
	if doc == nil || doc.Status != types.TemporaryDocumentStatusReady {
		return nil, fmt.Errorf("attachment is not ready")
	}
	images := doc.ViewableImages()
	if number < 1 || number > len(images) {
		return nil, fmt.Errorf("image number out of range")
	}
	if r.readImage == nil {
		return nil, fmt.Errorf("attachment image storage is unavailable")
	}
	data, err := r.readImage(ctx, images[number-1].URL)
	if err != nil {
		return nil, err
	}
	prepared, err := imageprep.Prepare(data, searchutil.VisionImageLimits)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare image %d: %w", number, err)
	}
	result := &interfaces.AttachmentImage{DataURI: prepared.DataURI()}
	var description *Description
	var key string
	if r.Images != nil && r.Images.Describe != nil {
		text := ""
		result.Description = &text
		description, key, err = r.Images.describe(ctx, doc.ID, prepared.Data, detail, focus)
		if description != nil {
			*result.Description = description.Text
		}
		if err != nil {
			result.Warning = "Image description: " + err.Error()
		}
	}
	if r.Projection != nil {
		result.Files, err = r.Projection.Image(ctx, doc, number, prepared.Data, prepared.MIMEType, key, description)
		if err != nil {
			result.Warning = strings.TrimSpace(result.Warning + " Could not sync image to sandbox: " + err.Error())
		}
	}
	return result, nil
}

func (r *ImageReader) describe(
	ctx context.Context, documentID string, data []byte, detail, focus string,
) (*Description, string, error) {
	detail, focus = types.NormalizeImageRead(detail, focus)
	prompt := types.ImageReadPrompt(detail, focus)
	identity, _ := json.Marshal([]string{r.Model, prompt, fmt.Sprintf("%x", sha256.Sum256(data))})
	key := fmt.Sprintf("%x", sha256.Sum256(identity))
	// Coalesce duplicate reads in one engine; the durable cache also serves
	// subsequent turns and forked sessions after their own authorization.
	result := r.calls.DoChan(documentID+":"+key, func() (interface{}, error) {
		if r.Cache != nil {
			cached, err := r.Cache.Get(ctx, documentID, key)
			if err == nil && cached != nil {
				return cached, nil
			}
		}
		imageCtx, cancel := context.WithTimeout(ctx, imageDescribeTimeout)
		defer cancel()
		r.slotsOnce.Do(func() { r.slots = make(chan struct{}, 4) })
		select {
		case r.slots <- struct{}{}:
			defer func() { <-r.slots }()
		case <-imageCtx.Done():
			return nil, imageCtx.Err()
		}
		text, err := r.Describe(imageCtx, data, prompt)
		if err != nil {
			return nil, err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, fmt.Errorf("vision model returned no description")
		}
		value := &Description{Text: text, Detail: detail, Focus: focus, Model: r.Model, CreatedAt: time.Now().UTC()}
		if r.Cache != nil {
			if err := r.Cache.Put(ctx, documentID, key, *value); err != nil {
				return value, fmt.Errorf("save description: %w", err)
			}
		}
		return value, nil
	})
	select {
	case <-ctx.Done():
		return nil, key, ctx.Err()
	case value := <-result:
		description, _ := value.Val.(*Description)
		return description, key, value.Err
	}
}
