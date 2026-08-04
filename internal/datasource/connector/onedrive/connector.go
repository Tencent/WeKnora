package onedrive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
)

const rootItemID = "root"

var supportedExtensions = map[string]struct{}{
	".txt": {}, ".md": {}, ".markdown": {}, ".pdf": {}, ".doc": {}, ".docx": {},
	".ppt": {}, ".pptx": {}, ".xls": {}, ".xlsx": {}, ".csv": {}, ".html": {},
	".htm": {}, ".json": {}, ".mp3": {}, ".wav": {}, ".m4a": {}, ".flac": {}, ".ogg": {},
}

// Connector synchronizes a delegated user's OneDrive into a knowledge base.
type Connector struct {
	items      interfaces.DataSourceItemRepository
	oauth      *datasource.OneDriveOAuthManager
	graphBase  string
	httpClient *http.Client
}

var (
	_ datasource.OAuthConnector               = (*Connector)(nil)
	_ datasource.RuntimeConnector             = (*Connector)(nil)
	_ datasource.ConnectionLifecycleConnector = (*Connector)(nil)
	_ datasource.SyncLifecycleConnector       = (*Connector)(nil)
	_ datasource.DeferredCommitConnector      = (*Connector)(nil)
	_ datasource.FetchResultConnector         = (*Connector)(nil)
)

// NewConnector creates a production OneDrive connector.
func NewConnector(items interfaces.DataSourceItemRepository, oauth *datasource.OneDriveOAuthManager) *Connector {
	return &Connector{items: items, oauth: oauth, graphBase: defaultGraphBase}
}

// NewConnectorWithClient creates a connector with an injected Graph endpoint for tests.
func NewConnectorWithClient(
	items interfaces.DataSourceItemRepository, graphBase string, client *http.Client,
) *Connector {
	return &Connector{items: items, graphBase: strings.TrimRight(graphBase, "/"), httpClient: client}
}

// Type returns the connector registry key.
func (c *Connector) Type() string { return types.ConnectorTypeOneDrive }

// OAuthProvider returns the provider key used by data-source OAuth routes.
func (c *Connector) OAuthProvider() string { return "onedrive" }

// DeferCursorUntilIngestionCompletes prevents delta commits before parsing finishes.
func (c *Connector) DeferCursorUntilIngestionCompletes() bool { return true }

// PrepareRuntime injects data-source-scoped token callbacks into a sync run.
func (c *Connector) PrepareRuntime(
	_ context.Context, dataSource *types.DataSource, config *types.DataSourceConfig,
) error {
	if dataSource == nil || config == nil {
		return datasource.ErrInvalidConfig
	}
	if c.oauth == nil {
		return datasource.ErrOAuthNotConfigured
	}
	config.Runtime = &types.DataSourceRuntime{
		DataSourceID: dataSource.ID, TenantID: dataSource.TenantID,
		ConnectionVersion: dataSource.ConnectionVersion,
		AccessToken: func(ctx context.Context) (string, error) {
			return c.oauth.AccessToken(ctx, dataSource.TenantID, dataSource.ID, dataSource.ConnectionVersion)
		},
		RefreshAccessToken: func(ctx context.Context) (string, error) {
			return c.oauth.RefreshAccessToken(ctx, dataSource.TenantID, dataSource.ID, dataSource.ConnectionVersion)
		},
	}
	return nil
}

// EnsureReady verifies that the current connection is authorized and usable.
func (c *Connector) EnsureReady(ctx context.Context, dataSource *types.DataSource) error {
	if c.oauth == nil {
		return datasource.ErrOAuthNotConfigured
	}
	status, err := c.oauth.Status(ctx, dataSource.TenantID, dataSource.ID)
	if err != nil {
		return err
	}
	if status == nil || !status.Authorized || status.ReauthorizationNeeded ||
		status.ConnectionVersion != dataSource.ConnectionVersion {
		return datasource.ErrOAuthReauthorizationRequired
	}
	return nil
}

// Disconnect revokes local authorization state and invalidates queued work.
func (c *Connector) Disconnect(ctx context.Context, dataSource *types.DataSource) error {
	if c.oauth == nil {
		return datasource.ErrOAuthNotConfigured
	}
	return c.oauth.Revoke(ctx, dataSource.TenantID, dataSource.ID)
}

// IsRunCurrent reports whether a sync run still owns the active connection generation.
func (c *Connector) IsRunCurrent(ctx context.Context, dataSource *types.DataSource) (bool, error) {
	if c.oauth == nil {
		return false, datasource.ErrOAuthNotConfigured
	}
	status, err := c.oauth.Status(ctx, dataSource.TenantID, dataSource.ID)
	if err != nil {
		return false, err
	}
	return status != nil && status.Authorized && status.ConnectionVersion == dataSource.ConnectionVersion, nil
}

// ReconcileItems replays retained source deletions when deletion sync is re-enabled.
func (c *Connector) ReconcileItems(
	ctx context.Context, dataSource *types.DataSource, fetched []types.FetchedItem,
) ([]types.FetchedItem, error) {
	if !dataSource.SyncDeletions || c.items == nil {
		return fetched, nil
	}
	retained, err := c.items.ListRetainedDeleted(
		ctx, dataSource.TenantID, dataSource.ID, dataSource.ConnectionVersion,
	)
	if err != nil {
		return nil, err
	}
	seenDeletes := make(map[string]struct{})
	for _, item := range fetched {
		if item.IsDeleted {
			seenDeletes[item.ExternalID] = struct{}{}
		}
	}
	for _, retainedItem := range retained {
		if _, exists := seenDeletes[retainedItem.ExternalID]; exists {
			continue
		}
		fetched = append(fetched, types.FetchedItem{
			ExternalID: retainedItem.ExternalID, IsDeleted: true,
			SourceResourceID: retainedItem.SelectedRootID,
			Metadata:         map[string]string{"drive_id": retainedItem.DriveID, "item_id": retainedItem.ItemID},
		})
	}
	return fetched, nil
}

// MarkItemDeleted records that a source deletion was handled or retained.
func (c *Connector) MarkItemDeleted(
	ctx context.Context, dataSource *types.DataSource, item *types.FetchedItem,
) error {
	if c.items == nil {
		return nil
	}
	driveID, itemID := item.Metadata["drive_id"], item.Metadata["item_id"]
	if driveID == "" || itemID == "" {
		return fmt.Errorf("onedrive deletion is missing drive/item identity")
	}
	if err := c.items.MarkDeleted(ctx, dataSource.TenantID, dataSource.ID,
		dataSource.ConnectionVersion, driveID, itemID, time.Now().UTC()); err != nil {
		return err
	}
	if dataSource.SyncDeletions {
		return c.items.SetIngested(ctx, dataSource.TenantID, dataSource.ID,
			dataSource.ConnectionVersion, driveID, itemID, false)
	}
	return nil
}

// MarkItemIngested records successful handoff to the knowledge pipeline.
func (c *Connector) MarkItemIngested(
	ctx context.Context, dataSource *types.DataSource, item *types.FetchedItem,
) error {
	if c.items == nil {
		return nil
	}
	driveID, itemID := item.Metadata["drive_id"], item.Metadata["item_id"]
	if driveID == "" || itemID == "" {
		return fmt.Errorf("onedrive item is missing drive/item identity")
	}
	return c.items.SetIngested(ctx, dataSource.TenantID, dataSource.ID,
		dataSource.ConnectionVersion, driveID, itemID, true)
}

// ValidateStaticConfig validates persisted configuration without requiring OAuth.
func (c *Connector) ValidateStaticConfig(config *types.DataSourceConfig) error {
	if config == nil || config.Type != "" && config.Type != types.ConnectorTypeOneDrive {
		return datasource.ErrInvalidConfig
	}
	return nil
}

// Validate verifies the delegated Graph connection.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	client, err := c.client(config)
	if err != nil {
		return err
	}
	_, err = client.getDrive(ctx)
	return err
}

// ListResources lists a drive or one folder level for the resource picker.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	client, err := c.client(config)
	if err != nil {
		return nil, err
	}
	if parentID == "" {
		driveInfo, err := client.getDrive(ctx)
		if err != nil {
			return nil, err
		}
		name := driveInfo.Name
		if name == "" {
			name = "OneDrive"
		}
		return []types.Resource{{
			ExternalID: encodeRef(resourceRef{DriveID: driveInfo.ID, ItemID: rootItemID}),
			Name:       name, Type: "drive", URL: driveInfo.WebURL, HasChildren: true,
		}}, nil
	}
	parent, err := decodeRef(parentID)
	if err != nil {
		return nil, datasource.ErrResourceNotFound
	}
	children, err := client.listChildren(ctx, parent.DriveID, parent.ItemID)
	if err != nil {
		return nil, err
	}
	result := make([]types.Resource, 0, len(children))
	for _, child := range children {
		result = append(result, resourceFromItem(parent.DriveID, parentID, child))
	}
	return result, nil
}

// ResolveResourceAncestors returns ancestors needed to reveal saved selections.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	client, err := c.client(config)
	if err != nil {
		return nil, err
	}
	ancestors := make(map[string]struct{})
	for _, raw := range resourceIDs {
		ref, err := decodeRef(raw)
		if err != nil {
			return nil, datasource.ErrResourceNotFound
		}
		current := ref.ItemID
		for current != "" && current != rootItemID {
			item, err := client.getItem(ctx, ref.DriveID, current)
			if err != nil {
				return nil, err
			}
			parent := item.ParentReference.ID
			if parent == "" {
				parent = rootItemID
			}
			ancestors[encodeRef(resourceRef{DriveID: ref.DriveID, ItemID: parent})] = struct{}{}
			current = parent
		}
	}
	result := make([]string, 0, len(ancestors))
	for id := range ancestors {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

// FetchAll performs a full scan using the legacy connector result shape.
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	result, err := c.FetchAllResult(ctx, config, resourceIDs)
	if result == nil {
		return nil, err
	}
	return result.Items, err
}

// FetchIncremental consumes a delta cursor using the legacy connector result shape.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	result, err := c.FetchIncrementalResult(ctx, config, cursor)
	if result == nil {
		return nil, nil, err
	}
	return result.Items, result.NextCursor, err
}

// FetchAllResult performs a full scan, catches up concurrent delta changes, and returns a cursor.
func (c *Connector) FetchAllResult(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) (*types.FetchResult, error) {
	client, err := c.client(config)
	if err != nil {
		return nil, err
	}
	driveInfo, err := client.getDrive(ctx)
	if err != nil {
		return nil, err
	}
	refs, canonicalIDs, err := c.normalizeRefs(ctx, client, resourceIDs, driveInfo.ID)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("at least one OneDrive resource must be selected")
	}
	startDelta, err := client.latestDelta(ctx, driveInfo.ID)
	if err != nil {
		return nil, err
	}
	generation := uuid.NewString()
	result := &types.FetchResult{}
	for _, ref := range refs {
		selectedRoot := encodeRef(ref)
		if ref.ItemID == rootItemID {
			children, err := client.listChildren(ctx, ref.DriveID, rootItemID)
			if err != nil {
				return nil, err
			}
			if err := c.recordRoot(ctx, config, ref, selectedRoot, generation); err != nil {
				return nil, err
			}
			for i := range children {
				if err := c.walk(ctx, client, config, ref.DriveID, &children[i], selectedRoot, generation, result); err != nil {
					return result, err
				}
			}
			continue
		}
		item, err := client.getItem(ctx, ref.DriveID, ref.ItemID)
		if err != nil {
			return nil, err
		}
		if err := c.walk(ctx, client, config, ref.DriveID, item, selectedRoot, generation, result); err != nil {
			return result, err
		}
	}

	changes, deltaLink, err := client.delta(ctx, startDelta)
	if err != nil {
		return result, err
	}
	if err := c.applyChanges(ctx, client, config, driveInfo.ID, refs, generation, changes, result); err != nil {
		return result, err
	}
	missing, err := c.items.ListNotSeen(
		ctx, config.Runtime.TenantID, config.Runtime.DataSourceID, config.Runtime.ConnectionVersion, generation,
	)
	if err != nil {
		return result, err
	}
	for _, item := range missing {
		if item.SelectedRootID != "" && item.Ingested && item.DeletedAt == nil {
			result.Items = append(result.Items, deletedFetchedItem(item))
		} else if item.DeletedAt == nil {
			// Folders and never-ingested files have no knowledge-side effect to
			// defer, but must still leave the live hierarchy projection.
			if err := c.items.MarkDeleted(
				ctx, item.TenantID, item.DataSourceID, item.ConnectionVersion,
				item.DriveID, item.ItemID, time.Now().UTC(),
			); err != nil {
				return result, err
			}
		}
	}
	result.NextCursor, err = buildCursor(deltaLink, selectionHash(canonicalIDs), config.Runtime.ConnectionVersion)
	return result, err
}

// FetchIncrementalResult applies changes since the last committed delta cursor.
func (c *Connector) FetchIncrementalResult(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) (*types.FetchResult, error) {
	client, err := c.client(config)
	if err != nil {
		return nil, err
	}
	driveInfo, err := client.getDrive(ctx)
	if err != nil {
		return nil, err
	}
	refs, canonicalIDs, err := c.normalizeRefs(ctx, client, config.ResourceIDs, driveInfo.ID)
	if err != nil {
		return nil, err
	}
	state, err := parseCursor(cursor)
	if err != nil || state.DeltaLink == "" || state.SelectionHash != selectionHash(canonicalIDs) ||
		state.ConnectionVersion != config.Runtime.ConnectionVersion {
		return c.FetchAllResult(ctx, config, config.ResourceIDs)
	}
	changes, deltaLink, err := client.delta(ctx, state.DeltaLink)
	if err != nil {
		if isDeltaExpired(err) {
			return c.FetchAllResult(ctx, config, config.ResourceIDs)
		}
		return nil, err
	}
	result := &types.FetchResult{}
	if err := c.applyChanges(ctx, client, config, driveInfo.ID, refs, "", changes, result); err != nil {
		return result, err
	}
	result.NextCursor, err = buildCursor(deltaLink, state.SelectionHash, state.ConnectionVersion)
	return result, err
}

func (c *Connector) walk(
	ctx context.Context,
	client *graphClient,
	config *types.DataSourceConfig,
	driveID string,
	item *driveItem,
	selectedRoot, generation string,
	result *types.FetchResult,
) error {
	if item == nil {
		return nil
	}
	ingested := false
	existing, err := c.items.Find(
		ctx, config.Runtime.TenantID, config.Runtime.DataSourceID,
		config.Runtime.ConnectionVersion, driveID, item.ID,
	)
	if err != nil {
		return err
	}
	if existing != nil {
		ingested = existing.Ingested
	}
	if err := c.upsertRemoteItem(ctx, config, driveID, item, selectedRoot, generation, ingested); err != nil {
		return err
	}
	if item.Folder != nil {
		children, err := client.listChildren(ctx, driveID, item.ID)
		if err != nil {
			return err
		}
		for i := range children {
			if err := c.walk(ctx, client, config, driveID, &children[i], selectedRoot, generation, result); err != nil {
				return err
			}
		}
		return nil
	}
	if item.File == nil {
		return nil
	}
	fetched, warning := c.fetchFile(ctx, client, driveID, item, selectedRoot)
	if warning != nil {
		result.Warnings = append(result.Warnings, *warning)
		return nil
	}
	result.Items = append(result.Items, *fetched)
	return nil
}

func (c *Connector) applyChanges(
	ctx context.Context,
	client *graphClient,
	config *types.DataSourceConfig,
	defaultDriveID string,
	selected []resourceRef,
	generation string,
	changes []driveItem,
	result *types.FetchResult,
) error {
	last := make(map[string]driveItem)
	liveChanges := make(map[string]struct{})
	order := make([]string, 0, len(changes))
	for _, item := range changes {
		driveID := item.ParentReference.DriveID
		if driveID == "" {
			driveID = defaultDriveID
		}
		key := driveID + ":" + item.ID
		if _, ok := last[key]; !ok {
			order = append(order, key)
		}
		last[key] = item
	}
	for key, item := range last {
		if item.Deleted == nil {
			liveChanges[key] = struct{}{}
		}
	}
	queuedDeletes := make(map[string]struct{})
	for _, key := range order {
		item := last[key]
		driveID := item.ParentReference.DriveID
		if driveID == "" {
			driveID = defaultDriveID
		}
		existing, err := c.items.Find(ctx, config.Runtime.TenantID, config.Runtime.DataSourceID,
			config.Runtime.ConnectionVersion, driveID, item.ID)
		if err != nil {
			return err
		}
		if item.Deleted != nil {
			if existing != nil && existing.SelectedRootID != "" {
				if err := c.queueRemoval(
					ctx, config, existing, liveChanges, queuedDeletes, result,
				); err != nil {
					return err
				}
			}
			continue
		}

		selectedRoot, err := c.resolveSelectedRoot(ctx, client, config, driveID, &item, selected)
		if err != nil {
			return err
		}
		if selectedRoot == "" {
			if existing != nil && existing.SelectedRootID != "" {
				if err := c.queueRemoval(
					ctx, config, existing, liveChanges, queuedDeletes, result,
				); err != nil {
					return err
				}
			}
			if err := c.upsertRemoteItem(ctx, config, driveID, &item, "", generation, false); err != nil {
				return err
			}
			continue
		}
		if item.Folder != nil {
			if err := c.walk(ctx, client, config, driveID, &item, selectedRoot, generation, result); err != nil {
				return err
			}
			continue
		}
		ingested := existing != nil && existing.Ingested
		if err := c.upsertRemoteItem(ctx, config, driveID, &item, selectedRoot, generation, ingested); err != nil {
			return err
		}
		fetched, warning := c.fetchFile(ctx, client, driveID, &item, selectedRoot)
		if warning != nil {
			result.Warnings = append(result.Warnings, *warning)
			continue
		}
		result.Items = append(result.Items, *fetched)
	}
	return nil
}

func (c *Connector) queueRemoval(
	ctx context.Context,
	config *types.DataSourceConfig,
	root *types.DataSourceItem,
	liveChanges map[string]struct{},
	queued map[string]struct{},
	result *types.FetchResult,
) error {
	items := []*types.DataSourceItem{root}
	if root.ItemType == "folder" {
		descendants, err := c.descendants(ctx, config, root.ItemID)
		if err != nil {
			return err
		}
		items = append(items, descendants...)
	}
	byID := make(map[string]*types.DataSourceItem, len(items))
	for _, item := range items {
		byID[item.ItemID] = item
	}
	deletedAt := time.Now().UTC()
	for _, item := range items {
		if item != root && survivesDeletedAncestor(item, root.ItemID, liveChanges, byID) {
			continue
		}
		if item.Ingested {
			if _, exists := queued[item.ExternalID]; !exists {
				result.Items = append(result.Items, deletedFetchedItem(item))
				queued[item.ExternalID] = struct{}{}
			}
			continue
		}
		if err := c.items.MarkDeleted(
			ctx, config.Runtime.TenantID, config.Runtime.DataSourceID,
			config.Runtime.ConnectionVersion, item.DriveID, item.ItemID, deletedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func survivesDeletedAncestor(
	item *types.DataSourceItem,
	deletedRootID string,
	liveChanges map[string]struct{},
	byID map[string]*types.DataSourceItem,
) bool {
	current := item
	seen := make(map[string]struct{})
	for current != nil && current.ItemID != deletedRootID {
		if _, live := liveChanges[current.DriveID+":"+current.ItemID]; live {
			return true
		}
		if _, cycle := seen[current.ItemID]; cycle {
			return false
		}
		seen[current.ItemID] = struct{}{}
		current = byID[current.ParentItemID]
	}
	return false
}

func (c *Connector) resolveSelectedRoot(
	ctx context.Context,
	client *graphClient,
	config *types.DataSourceConfig,
	driveID string,
	item *driveItem,
	selected []resourceRef,
) (string, error) {
	selectedByItem := make(map[string]string, len(selected))
	selectedIDs := make(map[string]struct{}, len(selected))
	for _, ref := range selected {
		if ref.DriveID == driveID {
			selectedByItem[ref.ItemID] = encodeRef(ref)
			selectedIDs[encodeRef(ref)] = struct{}{}
		}
	}
	if root := selectedByItem[item.ID]; root != "" {
		return root, nil
	}
	parent := item.ParentReference.ID
	seen := map[string]struct{}{item.ID: {}}
	for parent != "" {
		if root := selectedByItem[parent]; root != "" {
			return root, nil
		}
		if parent == rootItemID {
			return selectedByItem[rootItemID], nil
		}
		if _, duplicate := seen[parent]; duplicate {
			return "", fmt.Errorf("cycle detected in OneDrive parent chain")
		}
		seen[parent] = struct{}{}
		indexed, err := c.items.Find(ctx, config.Runtime.TenantID, config.Runtime.DataSourceID,
			config.Runtime.ConnectionVersion, driveID, parent)
		if err != nil {
			return "", err
		}
		if indexed != nil {
			if _, stillSelected := selectedIDs[indexed.SelectedRootID]; stillSelected {
				return indexed.SelectedRootID, nil
			}
			parent = indexed.ParentItemID
			continue
		}
		remote, err := client.getItem(ctx, driveID, parent)
		if err != nil {
			if isNotFound(err) {
				return "", nil
			}
			return "", err
		}
		parent = remote.ParentReference.ID
	}
	return "", nil
}

func (c *Connector) fetchFile(
	ctx context.Context, client *graphClient, driveID string, item *driveItem, selectedRoot string,
) (*types.FetchedItem, *types.FetchWarning) {
	ext := strings.ToLower(filepath.Ext(item.Name))
	if _, ok := supportedExtensions[ext]; !ok {
		return nil, &types.FetchWarning{
			Code: "unsupported_file_type", ExternalID: externalID(driveID, item.ID),
			Message: "unsupported file type: " + ext,
		}
	}
	maxSize := utils.GetMaxFileSize()
	if item.Size > maxSize {
		return nil, &types.FetchWarning{
			Code: "file_too_large", ExternalID: externalID(driveID, item.ID),
			Message: fmt.Sprintf("file exceeds %d bytes", maxSize),
		}
	}
	content, err := client.download(ctx, driveID, item.ID, maxSize)
	if err != nil {
		return &types.FetchedItem{
			ExternalID: externalID(driveID, item.ID), Title: item.Name, FileName: item.Name,
			Metadata:         map[string]string{"error": err.Error(), "drive_id": driveID, "item_id": item.ID},
			SourceResourceID: selectedRoot,
		}, nil
	}
	contentType := "application/octet-stream"
	if item.File != nil && item.File.MimeType != "" {
		contentType = item.File.MimeType
	} else if inferred := mime.TypeByExtension(ext); inferred != "" {
		contentType = inferred
	}
	return &types.FetchedItem{
		ExternalID: externalID(driveID, item.ID), Title: item.Name, Content: content,
		ContentType: contentType, FileName: item.Name, URL: item.WebURL, UpdatedAt: item.LastModifiedDateTime,
		Metadata: map[string]string{"drive_id": driveID, "item_id": item.ID}, SourceResourceID: selectedRoot,
	}, nil
}

func (c *Connector) upsertRemoteItem(
	ctx context.Context,
	config *types.DataSourceConfig,
	driveID string,
	item *driveItem,
	selectedRoot, generation string,
	ingested bool,
) error {
	itemType := "file"
	if item.Folder != nil {
		itemType = "folder"
	}
	parent := item.ParentReference.ID
	if parent == "" {
		parent = rootItemID
	}
	return c.items.Upsert(ctx, &types.DataSourceItem{
		TenantID: config.Runtime.TenantID, DataSourceID: config.Runtime.DataSourceID,
		ConnectionVersion: config.Runtime.ConnectionVersion, DriveID: driveID, ItemID: item.ID,
		ParentItemID: parent, ItemType: itemType, SelectedRootID: selectedRoot,
		ExternalID: externalID(driveID, item.ID), LastModifiedAt: item.LastModifiedDateTime,
		LastSeenGeneration: generation, Ingested: ingested,
	})
}

func (c *Connector) recordRoot(
	ctx context.Context, config *types.DataSourceConfig, ref resourceRef, selectedRoot, generation string,
) error {
	return c.items.Upsert(ctx, &types.DataSourceItem{
		TenantID: config.Runtime.TenantID, DataSourceID: config.Runtime.DataSourceID,
		ConnectionVersion: config.Runtime.ConnectionVersion, DriveID: ref.DriveID, ItemID: rootItemID,
		ItemType: "drive", SelectedRootID: selectedRoot, ExternalID: externalID(ref.DriveID, rootItemID),
		LastSeenGeneration: generation,
	})
}

func (c *Connector) descendants(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]*types.DataSourceItem, error) {
	children, err := c.items.ListByParent(ctx, config.Runtime.TenantID, config.Runtime.DataSourceID,
		config.Runtime.ConnectionVersion, parentID)
	if err != nil {
		return nil, err
	}
	result := make([]*types.DataSourceItem, 0, len(children))
	for _, child := range children {
		result = append(result, child)
		if child.ItemType == "folder" {
			nested, err := c.descendants(ctx, config, child.ItemID)
			if err != nil {
				return nil, err
			}
			result = append(result, nested...)
		}
	}
	return result, nil
}

func (c *Connector) client(config *types.DataSourceConfig) (*graphClient, error) {
	if err := c.ValidateStaticConfig(config); err != nil {
		return nil, err
	}
	if config.Runtime == nil || config.Runtime.AccessToken == nil ||
		config.Runtime.DataSourceID == "" || config.Runtime.TenantID == 0 {
		return nil, datasource.ErrOAuthReauthorizationRequired
	}
	if c.items == nil {
		return nil, fmt.Errorf("onedrive item repository is not configured")
	}
	return newGraphClient(c.graphBase, c.httpClient, config.Runtime.AccessToken, config.Runtime.RefreshAccessToken), nil
}

func (c *Connector) normalizeRefs(
	ctx context.Context, client *graphClient, ids []string, authorizedDriveID string,
) ([]resourceRef, []string, error) {
	refs := make([]resourceRef, 0, len(ids))
	seen := make(map[string]struct{})
	for _, raw := range ids {
		ref, err := decodeRef(raw)
		if err != nil || ref.DriveID != authorizedDriveID || ref.ItemID == "" {
			return nil, nil, datasource.ErrResourceNotFound
		}
		encoded := encodeRef(ref)
		if _, ok := seen[encoded]; ok {
			continue
		}
		seen[encoded] = struct{}{}
		refs = append(refs, ref)
	}
	rootRef := resourceRef{DriveID: authorizedDriveID, ItemID: rootItemID}
	if _, wholeDriveSelected := seen[encodeRef(rootRef)]; wholeDriveSelected {
		root := resourceRef{DriveID: authorizedDriveID, ItemID: rootItemID}
		encoded := encodeRef(root)
		return []resourceRef{root}, []string{encoded}, nil
	}

	// The UI normally sends a minimal cover set, but the API must enforce the
	// same invariant. Remove a child whenever one of its ancestors is selected.
	minimal := make([]resourceRef, 0, len(refs))
	for _, ref := range refs {
		covered := false
		current := ref.ItemID
		visited := map[string]struct{}{current: {}}
		for current != "" && current != rootItemID {
			item, err := client.getItem(ctx, ref.DriveID, current)
			if err != nil {
				if isNotFound(err) {
					return nil, nil, datasource.ErrResourceNotFound
				}
				return nil, nil, err
			}
			parent := item.ParentReference.ID
			if parent == "" {
				parent = rootItemID
			}
			if _, ok := seen[encodeRef(resourceRef{DriveID: ref.DriveID, ItemID: parent})]; ok {
				covered = true
				break
			}
			if _, cycle := visited[parent]; cycle {
				return nil, nil, fmt.Errorf("cycle detected in OneDrive selection hierarchy")
			}
			visited[parent] = struct{}{}
			current = parent
		}
		if !covered {
			minimal = append(minimal, ref)
		}
	}
	canonical := make([]string, 0, len(minimal))
	for _, ref := range minimal {
		canonical = append(canonical, encodeRef(ref))
	}
	sort.Strings(canonical)
	return minimal, canonical, nil
}

func resourceFromItem(driveID, parentID string, item driveItem) types.Resource {
	itemType := "file"
	if item.Folder != nil {
		itemType = "folder"
	}
	return types.Resource{
		ExternalID: encodeRef(resourceRef{DriveID: driveID, ItemID: item.ID}), Name: item.Name,
		Type: itemType, URL: item.WebURL, ModifiedAt: item.LastModifiedDateTime,
		ParentID: parentID, HasChildren: item.Folder != nil,
		Metadata: map[string]interface{}{"size": item.Size},
	}
}

func encodeRef(ref resourceRef) string {
	data, _ := json.Marshal(ref)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeRef(value string) (resourceRef, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return resourceRef{}, err
	}
	var ref resourceRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return resourceRef{}, err
	}
	if ref.DriveID == "" || ref.ItemID == "" {
		return resourceRef{}, errors.New("invalid OneDrive resource ID")
	}
	return ref, nil
}

func externalID(driveID, itemID string) string {
	return encodeRef(resourceRef{DriveID: driveID, ItemID: itemID})
}

func selectionHash(canonical []string) string {
	hash := sha256.Sum256([]byte(strings.Join(canonical, "\n")))
	return hex.EncodeToString(hash[:])
}

func buildCursor(deltaLink, hash string, version uint64) (*types.SyncCursor, error) {
	state := cursorState{DeltaLink: deltaLink, SelectionHash: hash, ConnectionVersion: version}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, err
	}
	return &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: generic}, nil
}

func parseCursor(cursor *types.SyncCursor) (cursorState, error) {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return cursorState{}, errors.New("cursor is empty")
	}
	data, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		return cursorState{}, err
	}
	var state cursorState
	err = json.Unmarshal(data, &state)
	return state, err
}

func deletedFetchedItem(item *types.DataSourceItem) types.FetchedItem {
	return types.FetchedItem{
		ExternalID: item.ExternalID, IsDeleted: true, SourceResourceID: item.SelectedRootID,
		Metadata: map[string]string{"drive_id": item.DriveID, "item_id": item.ItemID},
	}
}

func datasourceRandomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
