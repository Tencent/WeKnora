package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	files "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type attachmentLifecycleRepo struct {
	interfaces.TemporaryDocumentRepository
	docs      map[string]*types.TemporaryDocument
	expiredID string
	createErr error
}

func (r *attachmentLifecycleRepo) GetScoped(
	_ context.Context, _ uint64, _, id string,
) (*types.TemporaryDocument, error) {
	return r.docs[id], nil
}

func (r *attachmentLifecycleRepo) DeleteScoped(_ context.Context, _ uint64, _, id string) error {
	delete(r.docs, id)
	return nil
}

func (r *attachmentLifecycleRepo) ListExpired(context.Context, time.Time, int) ([]*types.TemporaryDocument, error) {
	if d := r.docs[r.expiredID]; d != nil {
		return []*types.TemporaryDocument{d}, nil
	}
	return nil, nil
}

func (r *attachmentLifecycleRepo) Create(_ context.Context, d *types.TemporaryDocument) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.docs[d.ID] = d
	return nil
}

type failingAttachmentBinding struct {
	interfaces.ResourceCatalog
	releaseErr error
}

func (c *failingAttachmentBinding) Bind(context.Context, string, string, string, string) error {
	return errors.New("binding failed")
}

func (c *failingAttachmentBinding) Release(ctx context.Context, ref, owner, id string) (int64, error) {
	if c.releaseErr != nil {
		return -1, c.releaseErr
	}
	return c.ResourceCatalog.Release(ctx, ref, owner, id)
}

func TestTemporaryDocumentReleaseDeduplicatedImages(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "delete"
		if expired {
			name = "expiry"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			catalog, _ := newResourceCatalogForTest(t)
			fs := files.NewResourceCatalogFileService(files.NewLocalFileService(t.TempDir(), ""), catalog)
			payload := []byte("identical image content")
			a, err := fs.SaveBytes(ctx, payload, 7, "first.png", false)
			require.NoError(t, err)
			b, err := fs.SaveBytes(ctx, payload, 7, "second.png", false)
			require.NoError(t, err)
			require.Equal(t, a, b)
			require.NoError(t, catalog.Bind(
				ctx, a, types.ResourceOwnerTemporaryDocument, "first", types.ResourceRelationSourceFile,
			))
			require.NoError(t, catalog.Bind(
				ctx, b, types.ResourceOwnerTemporaryDocument, "second", types.ResourceRelationSourceFile,
			))
			images, _ := json.Marshal([]types.TemporaryDocumentImage{{URL: a}, {URL: a}})
			repo := &attachmentLifecycleRepo{expiredID: "first", docs: map[string]*types.TemporaryDocument{
				"first":  {ID: "first", TenantID: 7, SessionID: "session", ResourceRef: a, ImageRefs: images},
				"second": {ID: "second", TenantID: 7, SessionID: "session", ResourceRef: b},
			}}
			svc := &temporaryDocumentService{fileService: fs, resourceCatalog: catalog, repo: repo}
			if expired {
				require.NoError(t, svc.CleanupExpired(ctx))
			} else {
				require.NoError(t, svc.Delete(ctx, 7, "session", "first"))
			}
			file, err := fs.GetFile(ctx, b)
			require.NoError(t, err)
			require.NoError(t, file.Close())
			require.NoError(t, svc.Delete(ctx, 7, "session", "second"))
			_, err = fs.GetFile(ctx, b)
			require.Error(t, err, "last owner must reclaim the image")
		})
	}
}

func TestTemporaryDocumentRollbackKeepsSharedKnowledgeImage(t *testing.T) {
	for _, failBind := range []bool{false, true} {
		name := "create failure"
		if failBind {
			name = "bind failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			catalog, _ := newResourceCatalogForTest(t)
			fs := files.NewResourceCatalogFileService(files.NewLocalFileService(t.TempDir(), ""), catalog)
			payload := []byte("shared image")
			ref, err := fs.SaveBytes(ctx, payload, 7, "knowledge.png", false)
			require.NoError(t, err)
			require.NoError(t, catalog.Bind(
				ctx, ref, types.ResourceOwnerKnowledge, "knowledge", types.ResourceRelationExtractedImage,
			))
			repo := &attachmentLifecycleRepo{docs: map[string]*types.TemporaryDocument{}}
			activeCatalog := catalog
			if failBind {
				activeCatalog = &failingAttachmentBinding{ResourceCatalog: catalog}
			} else {
				repo.createErr = errors.New("create failed")
			}
			svc := &temporaryDocumentService{fileService: fs, resourceCatalog: activeCatalog, repo: repo}
			_, err = svc.Create(
				ctx, 7, "session", "upload.png", "image/png", int64(len(payload)),
				bytes.NewReader(payload), types.TemporaryDocumentCreateOptions{},
			)
			require.Error(t, err)
			file, err := fs.GetFile(ctx, ref)
			require.NoError(t, err)
			require.NoError(t, file.Close())
			resource, err := catalog.Resolve(ctx, ref)
			require.NoError(t, err)
			require.Equal(t, types.ResourceStateActive, resource.State)
		})
	}
}

func TestTemporaryDocumentKeepsResourceWhenReleaseFails(t *testing.T) {
	ctx := context.Background()
	catalog, _ := newResourceCatalogForTest(t)
	fs := files.NewResourceCatalogFileService(files.NewLocalFileService(t.TempDir(), ""), catalog)
	ref, err := fs.SaveBytes(ctx, []byte("image"), 7, "image.png", false)
	require.NoError(t, err)
	svc := &temporaryDocumentService{
		fileService: fs,
		resourceCatalog: &failingAttachmentBinding{
			ResourceCatalog: catalog, releaseErr: errors.New("database unavailable"),
		},
	}
	require.Error(t, svc.releaseResource(ctx, "document", ref))
	file, err := fs.GetFile(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, file.Close())
}
