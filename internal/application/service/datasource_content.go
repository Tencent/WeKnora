package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"slices"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// DataSourceContentManager is the sole adapter between the data-source sync
// pipeline and knowledge ingestion. Keeping this boundary explicit prevents
// provider lifecycle code from leaking into the knowledge repository/service.
type DataSourceContentManager struct {
	knowledgeService interfaces.KnowledgeService
	tenantRepo       interfaces.TenantRepository
}

// NewDataSourceContentManager creates the provider-independent content adapter.
func NewDataSourceContentManager(
	knowledgeService interfaces.KnowledgeService,
	tenantRepo interfaces.TenantRepository,
) *DataSourceContentManager {
	return &DataSourceContentManager{knowledgeService: knowledgeService, tenantRepo: tenantRepo}
}

// WithTenant adds the workspace identity required by the knowledge pipeline.
func (m *DataSourceContentManager) WithTenant(ctx context.Context, tenantID uint64) (context.Context, error) {
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	if m.tenantRepo == nil {
		return ctx, nil
	}
	tenant, err := m.tenantRepo.GetTenantByID(ctx, tenantID)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, types.TenantInfoContextKey, tenant), nil
}

// DeleteByDataSource precisely removes content created by one data source.
// It is provider-independent and idempotent.
func (m *DataSourceContentManager) DeleteByDataSource(
	ctx context.Context, dataSource *types.DataSource,
) (int, error) {
	ctx, err := m.WithTenant(ctx, dataSource.TenantID)
	if err != nil {
		return 0, err
	}
	rows, err := m.knowledgeService.GetRepository().ListKnowledgeByKnowledgeBaseID(
		ctx, dataSource.TenantID, dataSource.KnowledgeBaseID,
	)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, knowledge := range rows {
		if knowledge == nil || knowledge.GetMetadata()["datasource_id"] != dataSource.ID {
			continue
		}
		if err := m.knowledgeService.DeleteKnowledge(ctx, knowledge.ID); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

type metadataFilterFinder interface {
	FindByMetadataFilters(context.Context, uint64, string, map[string]string) (*types.Knowledge, error)
}

// Find returns knowledge owned by this data source and external item identity.
func (m *DataSourceContentManager) Find(
	ctx context.Context, dataSource *types.DataSource, externalID string,
) (*types.Knowledge, error) {
	repo := m.knowledgeService.GetRepository()
	if finder, ok := repo.(metadataFilterFinder); ok {
		return finder.FindByMetadataFilters(ctx, dataSource.TenantID, dataSource.KnowledgeBaseID, map[string]string{
			"datasource_id": dataSource.ID,
			"external_id":   externalID,
		})
	}
	existing, err := repo.FindByMetadataKey(
		ctx, dataSource.TenantID, dataSource.KnowledgeBaseID, "external_id", externalID,
	)
	if err != nil || existing == nil {
		return existing, err
	}
	if existing.GetMetadata()["datasource_id"] != dataSource.ID {
		return nil, nil
	}
	return existing, nil
}

// DeleteItem idempotently removes knowledge corresponding to a fetched deletion.
func (m *DataSourceContentManager) DeleteItem(
	ctx context.Context, dataSource *types.DataSource, item *types.FetchedItem,
) error {
	existing, err := m.Find(ctx, dataSource, item.ExternalID)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	return m.knowledgeService.DeleteKnowledge(ctx, existing.ID)
}

// DeleteKnowledge removes one knowledge item by its internal identity.
func (m *DataSourceContentManager) DeleteKnowledge(ctx context.Context, knowledgeID string) error {
	return m.knowledgeService.DeleteKnowledge(ctx, knowledgeID)
}

// Ingest writes one fetched item through the normal knowledge pipeline and
// returns the created knowledge ID so cursor finalization can observe its
// asynchronous parse result.
func (m *DataSourceContentManager) Ingest(
	ctx context.Context,
	dataSource *types.DataSource,
	item *types.FetchedItem,
	tagIDs []string,
) (bool, string, error) {
	metadata := map[string]string{
		"external_id": item.ExternalID, "source_resource_id": item.SourceResourceID,
		"datasource_id": dataSource.ID,
	}
	for key, value := range item.Metadata {
		metadata[key] = value
	}

	isUpdate := false
	if item.ExternalID != "" {
		existing, err := m.Find(ctx, dataSource, item.ExternalID)
		if err != nil {
			return false, "", fmt.Errorf("find existing knowledge for external_id %s: %w", item.ExternalID, err)
		}
		if existing != nil {
			logger.Infof(
				ctx, "found existing knowledge %s for external_id=%s, deleting for update",
				existing.ID, item.ExternalID,
			)
			if err := m.knowledgeService.DeleteKnowledge(ctx, existing.ID); err != nil {
				logger.Warnf(ctx, "failed to delete existing knowledge %s: %v", existing.ID, err)
			} else {
				isUpdate = true
			}
		}
	}

	if len(item.Content) > 0 {
		fileHeader, err := bytesToFileHeader(item.Content, item.FileName)
		if err != nil {
			return isUpdate, "", fmt.Errorf("build file header: %w", err)
		}
		knowledge, err := m.knowledgeService.CreateKnowledgeFromFile(
			ctx, dataSource.KnowledgeBaseID, fileHeader, metadata, nil,
			item.FileName, tagIDs, dataSource.Type, nil,
		)
		if err != nil {
			var duplicate *types.DuplicateKnowledgeError
			if errors.As(err, &duplicate) && duplicateMatchesItem(duplicate, item) {
				m.sweepStaleSubtree(ctx, dataSource, item)
			}
			return isUpdate, "", err
		}
		if knowledge == nil {
			return isUpdate, "", fmt.Errorf("knowledge service returned no created item")
		}
		m.sweepStaleSubtree(ctx, dataSource, item)
		return isUpdate, knowledge.ID, nil
	}

	if item.URL != "" {
		knowledge, err := m.knowledgeService.CreateKnowledgeFromURL(
			ctx, dataSource.KnowledgeBaseID, item.URL, item.FileName, "", nil,
			item.Title, tagIDs, dataSource.Type, nil,
		)
		if err != nil {
			var duplicate *types.DuplicateKnowledgeError
			if errors.As(err, &duplicate) && duplicateMatchesItem(duplicate, item) {
				m.sweepStaleSubtree(ctx, dataSource, item)
			}
			return isUpdate, "", err
		}
		if knowledge == nil {
			return isUpdate, "", fmt.Errorf("knowledge service returned no created item")
		}
		m.sweepStaleSubtree(ctx, dataSource, item)
		return isUpdate, knowledge.ID, nil
	}

	return isUpdate, "", fmt.Errorf("item has neither content nor URL")
}

type metadataPrefixFinder interface {
	FindByMetadataKeyPrefix(context.Context, uint64, string, string, string) ([]*types.Knowledge, error)
}

func duplicateMatchesItem(duplicate *types.DuplicateKnowledgeError, item *types.FetchedItem) bool {
	return duplicate != nil && duplicate.Knowledge != nil &&
		duplicate.Knowledge.GetMetadata()["external_id"] == item.ExternalID
}

// sweepStaleSubtree reconciles child items only after the parent was written or
// confirmed by a same-node duplicate. This preserves the upstream datasource
// subtree contract without leaking it back into the generic sync orchestrator.
func (m *DataSourceContentManager) sweepStaleSubtree(
	ctx context.Context, dataSource *types.DataSource, item *types.FetchedItem,
) {
	if !item.ReplacesSubtree || item.ExternalID == "" {
		return
	}
	finder, ok := m.knowledgeService.GetRepository().(metadataPrefixFinder)
	if !ok {
		return
	}
	children, err := finder.FindByMetadataKeyPrefix(
		ctx, dataSource.TenantID, dataSource.KnowledgeBaseID,
		"external_id", types.SubtreeChildPrefix(item.ExternalID),
	)
	if err != nil {
		logger.Warnf(ctx, "failed to list subtree of external_id=%s: %v", item.ExternalID, err)
		return
	}
	ids := make([]string, 0, len(children))
	for _, child := range children {
		if child == nil || slices.Contains(item.SubtreeKeep, child.GetMetadata()["external_id"]) {
			continue
		}
		ids = append(ids, child.ID)
	}
	if len(ids) == 0 {
		return
	}
	if err := m.knowledgeService.DeleteKnowledgeList(ctx, ids); err != nil {
		logger.Warnf(ctx, "failed to delete %d stale sub-item(s) of external_id=%s: %v",
			len(ids), item.ExternalID, err)
	}
}

// Status returns the ingestion state for knowledge created by a sync run.
func (m *DataSourceContentManager) Status(
	ctx context.Context, tenantID uint64, knowledgeID string,
) (status string, message string, err error) {
	knowledge, err := m.knowledgeService.GetRepository().GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil || knowledge == nil {
		return "", "cannot read knowledge processing status", err
	}
	return knowledge.ParseStatus, knowledge.ErrorMessage, nil
}

func bytesToFileHeader(data []byte, filename string) (*multipart.FileHeader, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	header.Set("Content-Type", "application/octet-stream")
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, fmt.Errorf("create multipart part: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("write multipart data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}
	reader := multipart.NewReader(&buf, writer.Boundary())
	form, err := reader.ReadForm(int64(len(data)) + (1 << 20))
	if err != nil {
		return nil, fmt.Errorf("read multipart form: %w", err)
	}
	defer func() { _ = form.RemoveAll() }()
	files := form.File["file"]
	if len(files) == 0 {
		return nil, fmt.Errorf("multipart form contains no file")
	}
	return files[0], nil
}
