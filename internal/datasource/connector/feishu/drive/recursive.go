package drive

import (
	"context"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

func (o *driveOps) RecursiveSyncState() *core.RecursiveState {
	if !core.RecursiveLinksEnabled() {
		return nil
	}
	if o.recursiveState == nil {
		o.recursiveState = core.NewRecursiveState()
	}
	return o.recursiveState
}

func (o *driveOps) RefreshUnchanged(ctx context.Context, client *core.Client,
	n core.DriveFile, resourceID string,
) ([]*types.FetchedItem, error) {
	parent := core.LinkedResource{Type: n.Type, Token: n.Token, URL: n.URL}
	items, err := core.FetchLinkedChildren(ctx, client, parent, core.RecursiveFetchOptions{
		SourceResourceID: resourceID, ParentExternalID: n.Token, Channel: o.channel(),
		ParseMode: o.parseMode, State: o.RecursiveSyncState(),
	})
	qualifyItemFileNames(items, o.dirPaths[n.ParentToken])
	return items, err
}
