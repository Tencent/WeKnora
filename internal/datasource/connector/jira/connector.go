// Package jira imports Atlassian Jira projects and issues as Markdown.
package jira

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	_ datasource.StreamingConnector     = (*Connector)(nil)
	_ datasource.FullStreamingConnector = (*Connector)(nil)
)

// Connector implements datasource.StreamingConnector for Jira.
type Connector struct {
	newClient func(config) (*client, error)
}

// NewConnector creates a Jira connector.
func NewConnector() *Connector {
	return &Connector{newClient: newClient}
}

// Type returns the connector type identifier.
func (*Connector) Type() string {
	return types.ConnectorTypeJira
}

func (c *Connector) configured(ds *types.DataSourceConfig) (*client, config, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, config{}, err
	}
	factory := c.newClient
	if factory == nil {
		factory = newClient
	}
	cl, err := factory(cfg)
	return cl, cfg, err
}

// Validate pings Jira with the supplied credentials.
func (c *Connector) Validate(ctx context.Context, ds *types.DataSourceConfig) error {
	cl, _, err := c.configured(ds)
	if err != nil {
		return err
	}
	return cl.ping(ctx)
}

// ListResources returns the Jira projects visible to the credentials.
func (c *Connector) ListResources(
	ctx context.Context, ds *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	if parentID != "" {
		return nil, nil
	}
	cl, _, err := c.configured(ds)
	if err != nil {
		return nil, err
	}
	projects, err := cl.projects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]types.Resource, 0, len(projects))
	for _, p := range projects {
		name := p.Name
		if p.Key != "" {
			name = fmt.Sprintf("[%s] %s", p.Key, p.Name)
		}
		out = append(out, types.Resource{
			ExternalID:  p.Key,
			Name:        name,
			Type:        "project",
			URL:         cl.resourceURL(p.Key),
			HasChildren: false,
			Metadata: map[string]interface{}{
				"project_id":  p.ID,
				"project_key": p.Key,
			},
		})
	}
	return out, nil
}

// ResolveResourceAncestors is a no-op: Jira projects are a flat list.
func (*Connector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return nil, nil
}

// FetchAll syncs every selected project.
func (c *Connector) FetchAll(
	ctx context.Context, ds *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	dsCopy := *ds
	dsCopy.ResourceIDs = resourceIDs
	return c.collect(ctx, &dsCopy, nil)
}

// FetchIncremental syncs issues that changed since the last cursor.
func (c *Connector) FetchIncremental(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	items, next, err := c.collectWithCursor(ctx, ds, old)
	return items, next, err
}

type collector struct {
	items []types.FetchedItem
}

func (h *collector) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}

func (*collector) Checkpoint(context.Context, *types.SyncCursor) error {
	return nil
}

func (c *Connector) collect(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor,
) ([]types.FetchedItem, error) {
	items, _, err := c.collectWithCursor(ctx, ds, old)
	return items, err
}

func (c *Connector) collectWithCursor(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	h := &collector{}
	next, err := c.FetchStream(ctx, ds, old, h)
	return h.items, next, err
}

// FetchStream performs incremental or initial streaming sync.
func (c *Connector) FetchStream(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.fetchStream(ctx, ds, old, h, false)
}

// FetchFullStream forces all issues in selected projects to be re-evaluated.
func (c *Connector) FetchFullStream(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler,
) (*types.SyncCursor, error) {
	return c.fetchStream(ctx, ds, old, h, true)
}

func (c *Connector) fetchStream(
	ctx context.Context,
	ds *types.DataSourceConfig,
	old *types.SyncCursor,
	h datasource.StreamHandler,
	forceFull bool,
) (*types.SyncCursor, error) {
	if ds == nil || len(ds.ResourceIDs) == 0 {
		return nil, fmt.Errorf("jira requires at least one selected project")
	}
	cl, cfg, err := c.configured(ds)
	if err != nil {
		return nil, err
	}
	projects, err := cl.projects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Jira projects: %w", err)
	}

	byKeyOrID := make(map[string]project, len(projects)*2)
	for _, p := range projects {
		if p.Key != "" {
			byKeyOrID[p.Key] = p
		}
		if p.ID != "" {
			byKeyOrID[p.ID] = p
		}
	}

	baseline, next := prepareSyncCursors(old, forceFull)

	for _, resourceID := range ds.ResourceIDs {
		p, found := byKeyOrID[resourceID]
		if !found {
			return nil, fmt.Errorf(
				"selected Jira project %s is unavailable; refusing destructive reconciliation",
				resourceID,
			)
		}
		projKey := p.Key
		priorIssues, hadBaseline := baseline.ProjectIssues[projKey]
		if !hadBaseline {
			priorIssues, hadBaseline = baseline.ProjectIssues[p.ID]
		}
		if next.ProjectIssues[projKey] == nil {
			next.ProjectIssues[projKey] = map[string]string{}
		}

		// Build JQL
		jqlParts := []string{fmt.Sprintf("project = %q", projKey)}
		if cfg.jql != "" {
			jqlParts = append(jqlParts, fmt.Sprintf("(%s)", cfg.jql))
		}

		lastUpdated := baseline.LatestUpdated[projKey]
		isIncremental := !forceFull && hadBaseline && lastUpdated != ""
		if isIncremental {
			// Query only issues updated since the last known updated timestamp
			// In Jira JQL, updated >= 'YYYY-MM-DD HH:mm' or 'YYYY/MM/DD HH:mm'
			// To be safe with minute boundaries, parse time and format as 'YYYY-MM-DD HH:mm'
			if t := parseJiraTime(lastUpdated); !t.IsZero() {
				// Subtract 1 minute to avoid edge timing issues
				tFloor := t.Add(-1 * time.Minute)
				jqlParts = append(jqlParts, fmt.Sprintf("updated >= %q", tFloor.Format("2006-01-02 15:04")))
			}
		}

		finalJQL := strings.Join(jqlParts, " AND ") + " ORDER BY updated ASC, id ASC"

		nextPageToken := ""
		pageSize := defaultPageSize
		seen := make(map[string]struct{})

		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			searchResp, err := cl.searchIssues(ctx, finalJQL, nextPageToken, pageSize)
			if err != nil {
				return nil, fmt.Errorf("search issues in Jira project %s: %w", projKey, err)
			}

			if len(searchResp.Issues) == 0 {
				break
			}

			for _, iss := range searchResp.Issues {
				if err := ctx.Err(); err != nil {
					return nil, err
				}

				seen[iss.Key] = struct{}{}
				updatedStr := iss.Fields.Updated

				if !forceFull && hadBaseline {
					if priorUpdated, ok := priorIssues[iss.Key]; ok && priorUpdated == updatedStr {
						next.ProjectIssues[projKey][iss.Key] = updatedStr
						continue
					}
				}

				if cfg.includeComments {
					_ = cl.enrichCommentsIfNeeded(ctx, &iss)
				}

				// Download embeddable text attachments directly into the issue markdown
				for i, a := range iss.Fields.Attachment {
					if a.Content == "" {
						continue
					}
					ext := strings.ToLower(filepath.Ext(a.Filename))
					if embeddableTextExts[ext] == "" {
						continue
					}
					if a.Size > maxAttachmentBytes {
						continue
					}
					attData, err := cl.downloadAttachment(ctx, a.Content)
					if err != nil || len(attData) == 0 {
						continue
					}
					if utf8.Valid(attData) {
						contentStr := string(attData)
						if len(attData) > maxEmbeddedBytes {
							contentStr = string(attData[:maxEmbeddedBytes]) + fmt.Sprintf("\n... [Content truncated, total size %s, view full attachment on Jira]", humanFileSize(a.Size))
						}
						iss.Fields.Attachment[i].TextContent = contentStr
					}
				}

				md, err := issueToMarkdown(iss, cfg.baseURL, cfg.includeComments)
				if err != nil {
					failedItem := failedIssueItem(resourceID, projKey, iss, err)
					if emitErr := h.Emit(ctx, failedItem); emitErr != nil {
						return nil, emitErr
					}
					continue
				}

				item := types.FetchedItem{
					ExternalID:       iss.Key,
					Title:            fmt.Sprintf("[%s] %s", iss.Key, strings.TrimSpace(iss.Fields.Summary)),
					Content:          []byte(md),
					ContentType:      "text/markdown",
					FileName:         issueFileName(iss.Key, iss.Fields.Summary),
					URL:              cl.issueURL(iss.Key),
					CreatedAt:        parseJiraTime(iss.Fields.Created),
					UpdatedAt:        parseJiraTime(iss.Fields.Updated),
					SourceResourceID: resourceID,
					ReplacesSubtree:  true,
					SubtreeKeep:      nil,
					Metadata: map[string]string{
						"channel":     types.ChannelJira,
						"project_key": projKey,
						"issue_key":   iss.Key,
						"issue_type":  iss.Fields.IssueType.Name,
						"status":      iss.Fields.Status.Name,
					},
				}

				if err := h.Emit(ctx, item); err != nil {
					return nil, err
				}

				next.ProjectIssues[projKey][iss.Key] = updatedStr
				if updatedStr != "" {
					next.LatestUpdated[projKey] = updatedStr
				}

				if err := h.Checkpoint(ctx, next.syncCursor()); err != nil {
					return nil, err
				}
			}

			nextPageToken = searchResp.NextPageToken
			if searchResp.IsLast || len(searchResp.Issues) == 0 || nextPageToken == "" {
				break
			}
		}

		// Deletion reconciliation: only perform when full sync was executed across the whole project
		if hadBaseline && forceFull {
			missing := 0
			for id := range priorIssues {
				if _, exists := seen[id]; !exists {
					missing++
				}
			}
			if missing >= 20 && missing*100 >= len(priorIssues)*80 {
				return nil, fmt.Errorf(
					"refusing Jira mirror deletion in %s: %d/%d issues disappeared",
					projKey, missing, len(priorIssues),
				)
			}
			for id := range priorIssues {
				if _, exists := seen[id]; exists {
					continue
				}
				deleted := types.FetchedItem{
					ExternalID:       id,
					IsDeleted:        true,
					SourceResourceID: resourceID,
					Metadata: map[string]string{
						"channel":     types.ChannelJira,
						"project_key": projKey,
					},
				}
				if err := h.Emit(ctx, deleted); err != nil {
					return nil, err
				}
				delete(next.ProjectIssues[projKey], id)
				if err := h.Checkpoint(ctx, next.syncCursor()); err != nil {
					return nil, err
				}
			}
		}
	}

	next.FullSync = false
	next.FullSyncBaseline = nil
	return next.syncCursor(), nil
}

func classifyJiraError(err error) (code, reason string) {
	var api *apiError
	if errors.As(err, &api) {
		switch api.status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "jira_auth_or_permission",
				"Authentication or permission error; check credentials and project permissions"
		case http.StatusNotFound:
			return "jira_not_found", "Jira resource was not found; will retry on the next sync"
		case http.StatusTooManyRequests:
			return "jira_rate_limited", "Jira API rate limited; will retry on the next sync"
		default:
			if api.status >= 500 {
				return "jira_server_unavailable",
					"Jira service temporarily unavailable; will retry on the next sync"
			}
			return "jira_api_error", "Jira API error; see server logs"
		}
	}
	return "jira_sync_failed", "Jira issue could not be synced; see server logs"
}

func failedIssueItem(resourceID, projectKey string, iss issue, err error) types.FetchedItem {
	code, reason := classifyJiraError(err)
	return types.FetchedItem{
		ExternalID:       iss.Key,
		Title:            iss.Fields.Summary,
		SourceResourceID: resourceID,
		Metadata: map[string]string{
			"channel":           types.ChannelJira,
			"project_key":       projectKey,
			"issue_key":         iss.Key,
			"error":             err.Error(),
			"error_reason_code": code,
			"error_reason":      reason,
		},
	}
}
