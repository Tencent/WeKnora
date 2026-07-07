package cmdutil

import (
	"context"
	"time"

	sdk "github.com/Tencent/WeKnora/client"
)

// VisibleAgentLister enumerates current-tenant and shared-space agents.
type VisibleAgentLister interface {
	ListAgents(ctx context.Context) ([]sdk.Agent, error)
	ListSharedAgents(ctx context.Context) ([]sdk.SharedAgentInfo, error)
}

// VisibleAgent flattens shared-space metadata onto the ordinary Agent shape.
type VisibleAgent struct {
	sdk.Agent
	IsShared       bool       `json:"is_shared,omitempty"`
	ShareID        string     `json:"share_id,omitempty"`
	OrganizationID string     `json:"organization_id,omitempty"`
	OrgName        string     `json:"org_name,omitempty"`
	Permission     string     `json:"permission,omitempty"`
	SourceTenantID uint64     `json:"source_tenant_id,omitempty"`
	SharedAt       *time.Time `json:"shared_at,omitempty"`
	DisabledByMe   bool       `json:"disabled_by_me,omitempty"`
}

// ListVisibleAgents combines current-tenant and shared-space agents. Owned
// rows win if the same ID is ever returned by both endpoints.
func ListVisibleAgents(
	ctx context.Context, lister VisibleAgentLister, includeOwned, includeShared bool,
) ([]VisibleAgent, error) {
	var owned []sdk.Agent
	var shared []sdk.SharedAgentInfo
	if includeOwned {
		var err error
		owned, err = lister.ListAgents(ctx)
		if err != nil {
			return nil, err
		}
	}
	if includeShared {
		var err error
		shared, err = lister.ListSharedAgents(ctx)
		if err != nil {
			if includeOwned && ClassifyHTTPError(err) == CodeResourceNotFound {
				shared = nil
			} else {
				return nil, err
			}
		}
	}

	items := make([]VisibleAgent, 0, len(owned)+len(shared))
	seen := make(map[string]struct{}, len(owned)+len(shared))
	for _, agent := range owned {
		items = append(items, VisibleAgent{Agent: agent})
		if agent.ID != "" {
			seen[agent.ID] = struct{}{}
		}
	}
	for _, info := range shared {
		if info.Agent == nil {
			continue
		}
		if _, ok := seen[info.Agent.ID]; info.Agent.ID != "" && ok {
			continue
		}
		sharedAt := info.SharedAt
		items = append(items, VisibleAgent{
			Agent:          *info.Agent,
			IsShared:       true,
			ShareID:        info.ShareID,
			OrganizationID: info.OrganizationID,
			OrgName:        info.OrgName,
			Permission:     info.Permission,
			SourceTenantID: info.SourceTenantID,
			SharedAt:       &sharedAt,
			DisabledByMe:   info.DisabledByMe,
		})
		if info.Agent.ID != "" {
			seen[info.Agent.ID] = struct{}{}
		}
	}
	return items, nil
}
