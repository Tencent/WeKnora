package wiki

import (
	"context"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

func (o *wikiOps) RecursiveSyncState() *core.RecursiveState {
	if !core.RecursiveLinksEnabled() {
		return nil
	}
	if o.recursiveState == nil {
		o.recursiveState = core.NewRecursiveState()
	}
	return o.recursiveState
}

func (o *wikiOps) RefreshUnchanged(ctx context.Context, client *core.Client,
	n core.WikiNode, resourceID string,
) ([]*types.FetchedItem, error) {
	parent := core.LinkedResource{Type: n.ObjType, Token: n.ObjToken, URL: o.region.WikiURL(n.NodeToken)}
	items, err := core.FetchLinkedChildren(ctx, client, parent, core.RecursiveFetchOptions{
		SourceResourceID: resourceID, ParentExternalID: n.NodeToken, Channel: types.ChannelFeishu,
		ParseMode: o.parseMode, State: o.RecursiveSyncState(),
	})
	qualifyItemFileNames(items, o.dirPaths[n.NodeToken])
	return items, err
}
