package datasource

import "github.com/Tencent/WeKnora/internal/types"

// StreamStartCursor selects the cursor for a streaming synchronization run.
func StreamStartCursor(
	ds *types.DataSource,
	forceFull bool,
	attempt int,
	resumer ScheduledFullSyncResumer,
) (*types.SyncCursor, error) {
	if forceFull && attempt == 0 {
		if resumer != nil {
			cursor, err := ds.ParseSyncCursor()
			if err != nil {
				return nil, err
			}
			if resumer.ShouldResumeScheduledFullSync(cursor) {
				return cursor, nil
			}
		}
		return nil, nil
	}
	return ds.ParseSyncCursor()
}
