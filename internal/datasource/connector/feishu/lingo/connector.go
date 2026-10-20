package lingo

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	_ datasource.Connector               = (*Connector)(nil)
	_ datasource.ExistingDocumentAdopter = (*Connector)(nil)
)

// feishuEntityIDPattern matches the traceability line this connector (and the
// legacy Python sync tool) writes into every document body.
var feishuEntityIDPattern = regexp.MustCompile(`飞书词条\s*ID\s*[：:]\s*([A-Za-z0-9_-]+)`)

// Connector implements datasource.Connector for Feishu Lingo and, with the same
// code, for Lark. A core.Region picks the cloud, exactly like the wiki/drive
// connectors.
type Connector struct {
	region core.Region
	newAPI func(*core.Config) lingoAPI
}

// NewConnector creates a Lingo connector for the given region.
func NewConnector(region core.Region) *Connector {
	return &Connector{region: region, newAPI: newHTTPAPI}
}

// Type returns the connector type identifier.
func (c *Connector) Type() string { return c.region.ConnectorType }

// ExternalIDFromDocument recognises documents imported before this data source
// existed. Both this connector and the earlier Python sync tool write
// "飞书词条 ID：<entity_id>" into the body, so the first native sync can bind
// those rows instead of creating duplicate knowledge.
func (c *Connector) ExternalIDFromDocument(
	_ context.Context, _ *types.DataSourceConfig, _ string, content []byte,
) string {
	match := feishuEntityIDPattern.FindSubmatch(content)
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}

// Validate verifies credentials and the Lingo read scope with a one-item page.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return err
	}
	if _, err := c.newAPI(cfg).ListEntities(ctx, "", 1); err != nil {
		return fmt.Errorf("feishu lingo connection failed: %w", err)
	}
	return nil
}

// ListResources returns the Lingo repositories as selectable resources. Lingo
// is a flat list, so a non-empty parentID has no children.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	if parentID != "" {
		return []types.Resource{}, nil
	}
	cfg, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, err
	}
	repos, err := c.newAPI(cfg).ListRepos(ctx)
	if err != nil {
		// The entities endpoint can still be readable when the repo listing is
		// not (for example when the app was never added to the Lingo console).
		// Fall back to the organisation-wide dictionary so sync remains usable.
		logger.Warnf(ctx, "[Lingo] list repositories failed, falling back to default dictionary: %v", err)
		return []types.Resource{defaultResource()}, nil
	}
	if len(repos) == 0 {
		return []types.Resource{defaultResource()}, nil
	}
	sort.Slice(repos, func(i, j int) bool {
		if repos[i].Name == repos[j].Name {
			return repos[i].ID < repos[j].ID
		}
		return repos[i].Name < repos[j].Name
	})
	resources := make([]types.Resource, 0, len(repos))
	for _, repo := range repos {
		resources = append(resources, types.Resource{
			ExternalID: repo.ID,
			Name:       repo.Name,
			Type:       "repo",
		})
	}
	return resources, nil
}

func defaultResource() types.Resource {
	return types.Resource{
		ExternalID: defaultRepoID,
		Name:       "Default dictionary",
		Description: "Only the organisation-wide dictionary is visible. Add the app to each " +
			"repository's 词库设置 in Feishu Lingo to sync more repositories.",
		Type: "repo",
	}
}

// ResolveResourceAncestors is a no-op: Lingo resources are flat.
func (c *Connector) ResolveResourceAncestors(
	context.Context, *types.DataSourceConfig, []string,
) ([]string, error) {
	return []string{}, nil
}

// FetchAll returns every entity in the selected repositories.
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, err := c.fetch(ctx, config, resourceIDs, nil)
	return items, err
}

// FetchIncremental emits entities whose version changed and deletion events for
// entities that disappeared from the selected repositories.
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	previous := decodeLingoCursor(cursor)
	var resourceIDs []string
	if config != nil {
		resourceIDs = config.ResourceIDs
	}
	items, next, err := c.fetch(ctx, config, resourceIDs, previous)
	if err != nil {
		// Do not advance the cursor on failure: the next run retries the same work.
		return nil, nil, err
	}
	return items, next.encode(), nil
}

func (c *Connector) fetch(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	previous *lingoCursor,
) ([]types.FetchedItem, *lingoCursor, error) {
	cfg, err := core.ParseFeishuConfig(config, c.region)
	if err != nil {
		return nil, nil, err
	}
	cfgSettings, err := parseSettings(config)
	if err != nil {
		return nil, nil, err
	}
	api := c.newAPI(cfg)

	repoIDs, err := c.resolveRepoIDs(ctx, api, resourceIDs, config)
	if err != nil {
		return nil, nil, err
	}

	entities := make(map[string]Entity)
	entityRepo := make(map[string]string)
	for _, repoID := range repoIDs {
		batch, err := api.ListEntities(ctx, repoID, lingoPageSize)
		if err != nil {
			return nil, nil, fmt.Errorf("list lingo entities repo=%s: %w", repoID, err)
		}
		for _, entity := range batch {
			if strings.TrimSpace(entity.ID) == "" {
				continue
			}
			if _, exists := entities[entity.ID]; exists {
				continue
			}
			entities[entity.ID] = entity
			entityRepo[entity.ID] = repoID
		}
	}

	ids := make([]string, 0, len(entities))
	titleCounts := make(map[string]int, len(entities))
	for id, entity := range entities {
		ids = append(ids, id)
		titleCounts[entityTitle(entity)]++
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := entityTitle(entities[ids[i]]), entityTitle(entities[ids[j]])
		if left == right {
			return ids[i] < ids[j]
		}
		return left < right
	})

	next := &lingoCursor{
		LastSyncTime: time.Now().UTC(),
		EntityTimes:  make(map[string]string, len(entities)),
		EntityRepos:  make(map[string]string, len(entities)),
	}
	items := make([]types.FetchedItem, 0, len(entities))
	skipped := 0
	for _, id := range ids {
		entity := entities[id]
		version := entityVersion(entity)
		next.EntityTimes[id] = version
		next.EntityRepos[id] = entityRepo[id]
		if previous != nil && previous.EntityTimes[id] == version {
			continue
		}
		item, err := renderEntity(entity, entityRepo[id], cfgSettings, titleCounts)
		if err != nil {
			skipped++
			logger.Warnf(ctx, "[Lingo] skipping entity %s: %v", id, err)
			continue
		}
		items = append(items, item)
	}

	deleted := 0
	if previous != nil {
		scope := make(map[string]struct{}, len(repoIDs))
		for _, repoID := range repoIDs {
			scope[repoID] = struct{}{}
		}
		for id := range previous.EntityTimes {
			if _, exists := entities[id]; exists {
				continue
			}
			// Only reconcile deletions inside the repositories selected now.
			if repoID := previous.EntityRepos[id]; repoID != "" {
				if _, inScope := scope[repoID]; !inScope {
					continue
				}
			}
			items = append(items, types.FetchedItem{
				ExternalID:       id,
				IsDeleted:        true,
				UpdatedAt:        next.LastSyncTime,
				SourceResourceID: previous.EntityRepos[id],
			})
			deleted++
		}
	}

	logger.Infof(ctx, "[Lingo] repos=%d entities=%d emitted=%d skipped=%d deleted=%d",
		len(repoIDs), len(entities), len(items), skipped, deleted)
	return items, next, nil
}

func (c *Connector) resolveRepoIDs(
	ctx context.Context, api lingoAPI, resourceIDs []string, config *types.DataSourceConfig,
) ([]string, error) {
	selected := normalizeResourceIDs(resourceIDs)
	if len(selected) == 0 {
		selected = normalizeResourceIDs(config.ResourceIDs)
	}
	if len(selected) > 0 {
		return selected, nil
	}
	repos, err := api.ListRepos(ctx)
	if err != nil {
		logger.Warnf(ctx, "[Lingo] list repositories failed, using default dictionary: %v", err)
		return []string{defaultRepoID}, nil
	}
	for _, repo := range repos {
		if strings.TrimSpace(repo.ID) != "" {
			selected = append(selected, repo.ID)
		}
	}
	if len(selected) == 0 {
		return []string{defaultRepoID}, nil
	}
	return selected, nil
}

func normalizeResourceIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
