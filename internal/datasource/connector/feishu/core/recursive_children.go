package core

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// FetchLinkedChildren inspects table links even when the parent's edit time is unchanged.
func FetchLinkedChildren(ctx context.Context, client *Client,
	parent LinkedResource, options RecursiveFetchOptions,
) ([]*types.FetchedItem, error) {
	if !RecursiveLinksEnabled() || (parent.Type != "sheet" && parent.Type != "bitable") {
		return nil, nil
	}
	children, err := DiscoverDirectLinkedResources(ctx, client, parent)
	if err != nil {
		options.State.MarkIncomplete()
		return []*types.FetchedItem{{
			ExternalID:       options.ParentExternalID + "#recursive-discovery-error",
			SourceResourceID: options.SourceResourceID,
			Metadata: FeishuErrorItemMeta(err, map[string]string{
				"channel": options.Channel, "recursive": "true", "failure_stage": "recursive_discovery",
			}),
		}}, nil
	}
	options.SeedVisited = append(options.SeedVisited, parent)
	return FetchRecursiveLinkedResources(ctx, client, children, options)
}
