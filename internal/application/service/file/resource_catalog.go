package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// resourceCatalogFileService keeps provider drivers physical-path-only while
// exposing stable resource:// references to the application layer.
type resourceCatalogFileService struct {
	inner       interfaces.FileService
	catalog     interfaces.ResourceCatalog
	externalURL string
	global      bool
}

// NewGlobalResourceCatalogFileService pins writes to the process-wide driver's
// concrete instance, independently of workspace defaults and legacy aliases.
func NewGlobalResourceCatalogFileService(
	inner interfaces.FileService,
	catalog interfaces.ResourceCatalog,
) interfaces.FileService {
	svc := NewResourceCatalogFileService(inner, catalog)
	if wrapped, ok := svc.(*resourceCatalogFileService); ok {
		wrapped.global = true
	}
	return svc
}

// NewResourceCatalogFileService decorates a physical FileService with stable
// resource registration and resolution.
func NewResourceCatalogFileService(
	inner interfaces.FileService,
	catalog interfaces.ResourceCatalog,
) interfaces.FileService {
	if inner == nil || catalog == nil {
		return inner
	}
	return &resourceCatalogFileService{
		inner:       inner,
		catalog:     catalog,
		externalURL: strings.TrimRight(strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL")), "/"),
	}
}

func (s *resourceCatalogFileService) CheckConnectivity(ctx context.Context) error {
	return s.inner.CheckConnectivity(ctx)
}

func resourceKind(name string) (string, string) {
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	kind := "file"
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		kind = "image"
	case strings.HasPrefix(mimeType, "audio/"):
		kind = "audio"
	case strings.HasPrefix(mimeType, "video/"):
		kind = "video"
	}
	return kind, mimeType
}

func (s *resourceCatalogFileService) register(
	ctx context.Context,
	physical string,
	tenantID uint64,
	name string,
	size int64,
	temporary bool,
	contentHash string,
) (string, error) {
	kind, mimeType := resourceKind(name)
	register := s.catalog.Register
	if s.global {
		registrar, ok := s.catalog.(interfaces.GlobalResourceRegistrar)
		if !ok {
			_ = s.inner.DeleteFile(ctx, physical)
			return "", fmt.Errorf("global storage registration is unavailable")
		}
		register = registrar.RegisterGlobal
	}
	ref, err := register(ctx, tenantID, physical, interfaces.ResourceRegistration{
		Kind:         kind,
		MimeType:     mimeType,
		OriginalName: filepath.Base(name),
		Size:         size,
		ContentHash:  contentHash,
		Temporary:    temporary,
	})
	if err != nil {
		_ = s.inner.DeleteFile(ctx, physical)
		return "", fmt.Errorf("register stored resource: %w", err)
	}
	return ref, nil
}

func (s *resourceCatalogFileService) SaveFile(
	ctx context.Context,
	file *multipart.FileHeader,
	tenantID uint64,
	knowledgeID string,
) (string, error) {
	physical, err := s.inner.SaveFile(ctx, file, tenantID, knowledgeID)
	if err != nil {
		return "", err
	}
	ref, err := s.register(ctx, physical, tenantID, file.Filename, file.Size, false, "")
	if err != nil {
		return "", err
	}
	if knowledgeID != "" {
		if err := s.catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, knowledgeID, types.ResourceRelationSourceFile); err != nil {
			_ = s.DeleteFile(ctx, ref)
			return "", fmt.Errorf("bind stored resource: %w", err)
		}
	}
	return ref, nil
}

// isImagePayload reports whether data should be treated as image content.
//
// The rule is deliberately byte-first: two payloads can only be shared when
// their bytes are equal, so the decision must not hinge on a name the caller
// chose freely. The extension is still consulted as a fallback because some
// extracted image formats — SVG above all — have no signature
// http.DetectContentType recognises, and those are exactly the vector assets a
// document repeats across pages. A payload is therefore an image when its name
// maps to an image media type (the same rule register uses to derive Kind) or
// when its bytes sniff as image/*.
func isImagePayload(name string, data []byte) bool {
	if kind, _ := resourceKind(name); kind == "image" {
		return true
	}
	return strings.HasPrefix(http.DetectContentType(data), "image/")
}

func (s *resourceCatalogFileService) SaveBytes(
	ctx context.Context,
	data []byte,
	tenantID uint64,
	fileName string,
	temp bool,
) (string, error) {
	sum := sha256.Sum256(data)
	contentHash := hex.EncodeToString(sum[:])

	// Identical bytes pulled out of one document — the same logo or mascot on
	// several pages — used to be written once per occurrence and registered as
	// a separate handle each time: duplicated storage, the same picture
	// captioned repeatedly, and one identical gallery card per copy. Look the
	// payload up before writing so every occurrence shares one handle.
	//
	// Reuse is restricted to images outside temporary scratch space. For a
	// user upload or a generated artifact the original name is part of what
	// the caller asked for, and sharing would surface the first name for the
	// second file; extracted document images are named from a generated UUID,
	// so sharing one is invisible. Temporary objects are excluded because
	// their lifecycle is per-use.
	if !temp && isImagePayload(fileName, data) {
		ref, ok, err := s.catalog.ReuseByContentHash(ctx, tenantID, contentHash)
		if err == nil && ok {
			return ref, nil
		}
		// Fail open: a lookup failure or a miss must never stop bytes from
		// being stored, so fall through to the normal write-and-register path.
	}

	physical, err := s.inner.SaveBytes(ctx, data, tenantID, fileName, temp)
	if err != nil {
		return "", err
	}
	return s.register(ctx, physical, tenantID, fileName, int64(len(data)), temp, contentHash)
}

func (s *resourceCatalogFileService) resolve(ctx context.Context, value string) (string, bool, error) {
	physical, resource, err := s.catalog.ResolvePath(ctx, value)
	return physical, resource != nil, err
}

func (s *resourceCatalogFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	physical, _, err := s.resolve(ctx, filePath)
	if err != nil {
		return nil, err
	}
	return s.inner.GetFile(ctx, physical)
}

func (s *resourceCatalogFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	physical, isResource, err := s.resolve(ctx, filePath)
	if err != nil {
		return "", err
	}
	if isResource && s.externalURL != "" {
		token, grantErr := s.catalog.CreateAccessGrant(ctx, filePath, 2*time.Hour)
		if grantErr != nil {
			return "", grantErr
		}
		return s.externalURL + "/r/" + token, nil
	}
	return s.inner.GetFileURL(ctx, physical)
}

func (s *resourceCatalogFileService) DeleteFile(ctx context.Context, filePath string) error {
	physical, isResource, err := s.resolve(ctx, filePath)
	if err != nil {
		return err
	}
	if err := s.inner.DeleteFile(ctx, physical); err != nil {
		return err
	}
	if isResource {
		return s.catalog.MarkDeleted(ctx, filePath)
	}
	return nil
}

func (s *resourceCatalogFileService) CopyFile(
	ctx context.Context,
	filePath string,
	tenantID uint64,
	knowledgeID string,
) (string, error) {
	physical, _, err := s.resolve(ctx, filePath)
	if err != nil {
		return "", err
	}
	copied, err := s.inner.CopyFile(ctx, physical, tenantID, knowledgeID)
	if err != nil {
		return "", err
	}
	ref, err := s.register(ctx, copied, tenantID, filepath.Base(physical), 0, false, "")
	if err != nil {
		return "", err
	}
	if knowledgeID != "" {
		if err := s.catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, knowledgeID, types.ResourceRelationSourceFile); err != nil {
			_ = s.DeleteFile(ctx, ref)
			return "", fmt.Errorf("bind copied resource: %w", err)
		}
	}
	return ref, nil
}

var _ interfaces.FileService = (*resourceCatalogFileService)(nil)
