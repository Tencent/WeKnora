package session

import (
	"context"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// resolveArtifactFileService returns the file service that owns an artifact's
// bytes, plus a context scoped to the owning tenant.
//
// Artifacts are not always on the process-wide file service: a tenant can pin
// its workspace to its own storage backend, and the object then only exists
// there. Both reading and deleting an artifact have to go through the same
// resolution, which is why it lives here rather than inline in the download
// handler.
//
// ok is false when the owning tenant or its backend cannot be reached; callers
// treat that as "artifact unavailable" rather than falling back to the default
// service, which would look in the wrong bucket.
func (h *Handler) resolveArtifactFileService(
	ctx context.Context, ownerTenantID uint64, path, storageBackendID, label string,
) (interfaces.FileService, context.Context, bool) {
	ctx = types.WithExecutionTenant(ctx, ownerTenantID)
	if h.fileService == nil {
		return nil, ctx, false
	}
	if h.tenantService == nil {
		return h.fileService, ctx, true
	}
	tenant, err := h.tenantService.GetTenantByID(ctx, ownerTenantID)
	if err != nil || tenant == nil {
		return nil, ctx, false
	}
	backendID, providerPath, scoped := types.ParseStorageBackendPath(path)
	if !scoped {
		providerPath = path
	}
	if storageBackendID != "" {
		backendID = storageBackendID
	}
	fileService, _, ok := filesvc.ResolveTenantFileServiceWithFallback(
		ctx,
		label,
		tenant,
		backendID,
		types.ParseProviderScheme(providerPath),
		storageurl.LocalStorageBaseDir(),
		h.storageResolver,
		h.fileService,
	)
	if !ok {
		return nil, ctx, false
	}
	return fileService, ctx, true
}

// reclaimArtifactBlobs deletes the stored objects behind artifacts that were
// just tombstoned. Best-effort by design: the rows are already marked, and a
// blob left behind is recoverable (the tombstone still carries its url) while a
// failed request after a successful tombstone is not.
//
// A blob is only removed once the owning message's claim on it was the last
// one. An answer saved into a knowledge base, or a later turn that re-attached
// the same file, holds its own binding; deleting the bytes underneath it would
// break a document nobody asked to delete.
func (h *Handler) reclaimArtifactBlobs(ctx context.Context, tenantID uint64, refs []types.ArtifactBlobRef) {
	for _, ref := range refs {
		if ref.URL == "" {
			continue
		}
		path, ownerTenantID, backendID, ok := h.locateArtifactBlob(ctx, tenantID, ref)
		if !ok {
			continue
		}
		fileService, blobCtx, ok := h.resolveArtifactFileService(
			ctx, ownerTenantID, path, backendID, "artifact delete",
		)
		if !ok {
			logger.Warnf(ctx, "artifact delete: storage unavailable for %s, blob kept", ref.URL)
			continue
		}
		// DeleteFile takes the stored reference: the catalog-backed service
		// resolves it and marks the resource deleted once the bytes are gone.
		if err := fileService.DeleteFile(blobCtx, ref.URL); err != nil {
			logger.Warnf(ctx, "artifact delete: failed to remove blob %s: %v", ref.URL, err)
		}
	}
}

// locateArtifactBlob drops every deleted message's claim on a blob and, when
// nothing is left holding it, reports where the bytes live. ok is false
// whenever the blob must be kept — another owner still references it, or a
// release failed and we cannot tell.
//
// An unreadable binding count keeps the file, matching the knowledge-delete
// path: an orphaned blob is reclaimable later, a file that vanished from
// someone else's document is not.
func (h *Handler) locateArtifactBlob(
	ctx context.Context, tenantID uint64, ref types.ArtifactBlobRef,
) (path string, ownerTenantID uint64, backendID string, ok bool) {
	if h.resourceCatalog == nil {
		// No catalog: the url is a provider path in the caller's own tenant and
		// nothing else can be holding a reference to it.
		return ref.URL, tenantID, "", true
	}
	// -1 means the reference is not a catalog handle, i.e. there are no claims
	// to account for; only a non-negative count is a real answer.
	remaining := int64(-1)
	for _, messageID := range ref.MessageIDs {
		count, err := h.resourceCatalog.Release(ctx, ref.URL, types.ResourceOwnerMessage, messageID)
		if err != nil {
			logger.Warnf(ctx, "artifact delete: failed to release %s, blob kept: %v", ref.URL, err)
			return "", 0, "", false
		}
		if count >= 0 {
			remaining = count
		}
	}
	if remaining > 0 {
		logger.Infof(ctx, "artifact delete: keeping %s, %d owner(s) still reference it", ref.URL, remaining)
		return "", 0, "", false
	}
	resolved, resource, err := h.resourceCatalog.ResolvePath(ctx, ref.URL)
	if err != nil {
		logger.Warnf(ctx, "artifact delete: failed to resolve %s, blob kept: %v", ref.URL, err)
		return "", 0, "", false
	}
	if resource == nil {
		return resolved, tenantID, "", true
	}
	if resource.TenantID != tenantID {
		// The session is the caller's, so its artifacts should be too. A
		// mismatch means the url points somewhere unexpected; leave it alone.
		logger.Warnf(ctx, "artifact delete: %s belongs to tenant %d, not %d; blob kept",
			ref.URL, resource.TenantID, tenantID)
		return "", 0, "", false
	}
	return resolved, resource.TenantID, resource.StorageBackendID, true
}
