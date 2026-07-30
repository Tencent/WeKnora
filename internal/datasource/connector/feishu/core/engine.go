package core

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// engine.go holds the single generic streaming sync engine shared by the wiki
// Connector and the Drive DriveConnector. The per-connector differences (node
// type, listing API, edit-time field, cursor wire format, Fetch dispatch,
// log tag) are isolated behind the NodeOps adapter interface. FetchAll /
// FetchIncremental are thin wrappers over the same engine that collect Emits
// instead of streaming them.
//
// Behaviour note (deliberate, see ADR-0005 / design §2.4):
//   - Resume/incremental fast-path: a node recorded at its current edit time
//     is Skipped, keeping the cursor entry.
//   - A Fetch failure does NOT advance the cursor: the prior edit time is
//     retained so the node is retried next run instead of being permanently
//     Skipped on a transient export failure (Tencent/WeKnora#2136). This now
//     also holds for the FetchIncremental path (previously it advanced the
//     cursor before fetching, a latent #2136 bug).
//   - Logs use the "stream progress/summary" wording uniformly; the
//     FetchIncremental path additionally gains per-100 progress + tally
//     summary logs it did not emit before (log-only change).

// NodeOps adapts one connector's node type to the shared sync engine. Every
// method is a pure accessor or a thin wrapper - no engine logic lives here.
type NodeOps[N any] interface {
	// List returns every syncable node under resourceID. A non-nil partial
	// (with err == nil) signals a partial listing: nodes is still usable and
	// the sync continues, but the caller surfaces the failed sub-trees via
	// ListFailureItems. A non-nil err is fatal and aborts the sync.
	List(ctx context.Context, client *Client, resourceID string) (nodes []N, partial error, err error)

	Token(n N) string
	Title(n N) string
	ObjType(n N) string
	// EditTime is the change-detection timestamp string stored in the cursor.
	EditTime(n N) string

	// Fetch retrieves one node's content; (nil, nil) means an unsupported
	// type that yields no item.
	Fetch(ctx context.Context, client *Client, n N, resourceID string, multimodal bool) ([]*types.FetchedItem, error)

	// ListFailureItems converts a partial-listing error into error FetchedItems.
	ListFailureItems(resourceID string, partial error) []types.FetchedItem
	// ResourceNoun is the noun in user-visible error text: "nodes" / "files".
	ResourceNoun() string
	// EmptyResourceIDsError is the connector-specific message when no resource
	// IDs are configured (wiki/drive text differs and is preserved verbatim).
	EmptyResourceIDsError() string
	// LogTag is the log prefix: "[Feishu]" / "[FeishuDrive]".
	LogTag() string

	// DecodeCursorTimes extracts the per-resource edit-time map from a
	// persisted ConnectorCursor (nil-safe: returns nil when absent).
	DecodeCursorTimes(m map[string]interface{}) map[string]map[string]string
	// EncodeCursor wraps the engine's internal times map into the connector's
	// wire-format SyncCursor. JSON-marshals for snapshot isolation, mirroring
	// the original FeishuCursor.toSyncCursor / FeishuDriveCursor.toSyncCursor.
	EncodeCursor(times map[string]map[string]string, lastSync time.Time) *types.SyncCursor
}

// CollectHandler is the StreamHandler used by FetchAll / FetchIncremental to
// gather every Emitted item into a slice instead of streaming. Checkpoint is a
// no-op: those paths return a single cursor at the end.
type CollectHandler struct {
	items []types.FetchedItem
}

func (h *CollectHandler) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}

func (h *CollectHandler) Checkpoint(_ context.Context, _ *types.SyncCursor) error { return nil }

// runSync is the single implementation behind FetchStream / FetchAll /
// FetchIncremental. With cursor == nil it fetches everything (full sync);
// with a cursor it skips nodes whose recorded edit time is unchanged
// (incremental + resume). resourceIDs comes from the caller: FetchStream /
// FetchIncremental pass config.ResourceIDs (after a non-empty check),
// FetchAll passes its own argument without one.
func runSync[N any](
	ctx context.Context, client *Client, config *types.DataSourceConfig,
	resourceIDs []string, cursor *types.SyncCursor, h datasource.StreamHandler,
	ops NodeOps[N],
) (*types.SyncCursor, error) {
	var prevTimes map[string]map[string]string
	if cursor != nil && cursor.ConnectorCursor != nil {
		prevTimes = ops.DecodeCursorTimes(cursor.ConnectorCursor)
	}

	newTimes := make(map[string]map[string]string)
	lastSync := time.Now()

	processed := 0
	lastCheckpoint := time.Now()
	for _, resourceID := range resourceIDs {
		nodes, partial, err := ops.List(ctx, client, resourceID)
		if err != nil {
			return nil, fmt.Errorf("list %s for resource %s: %w", ops.ResourceNoun(), resourceID, err)
		}
		if partial != nil {
			for _, item := range ops.ListFailureItems(resourceID, partial) {
				if eerr := h.Emit(ctx, item); eerr != nil {
					return nil, eerr
				}
			}
		}

		newTimes[resourceID] = make(map[string]string)
		// On a partial listing, carry prior edit times forward so a later full
		// listing can still detect changes and deletions.
		if partial != nil && prevTimes != nil {
			if prev, ok := prevTimes[resourceID]; ok {
				for tok, et := range prev {
					newTimes[resourceID][tok] = et
				}
			}
		}

		currentNodes := make(map[string]bool)
		tally := NewFetchTally(len(nodes))
		for i, node := range nodes {
			tok := ops.Token(node)
			currentNodes[tok] = true
			editTimeStr := ops.EditTime(node)

			var prevEdit string
			var hadPrev bool
			if prevTimes != nil {
				if prev, ok := prevTimes[resourceID]; ok {
					prevEdit, hadPrev = prev[tok]
				}
			}

			// Resume/incremental fast-path: a node recorded at its current edit
			// time is unchanged (or already synced this run) - keep the record
			// and Skip re-fetching.
			if hadPrev && prevEdit == editTimeStr {
				newTimes[resourceID][tok] = editTimeStr
				continue
			}

			items, ferr := ops.Fetch(ctx, client, node, resourceID, config.MultimodalEnabled)
			if ferr != nil {
				tally.Fail()
				// Do NOT advance the cursor: the content was never fetched.
				// Retain the prior edit time (if any) so prev != current next
				// run and the node is retried, instead of being permanently
				// Skipped on a transient export failure (Tencent/WeKnora#2136).
				if hadPrev {
					newTimes[resourceID][tok] = prevEdit
				}
				if eerr := h.Emit(ctx, types.FetchedItem{
					ExternalID:       tok,
					Title:            ops.Title(node),
					SourceResourceID: resourceID,
					Metadata:         FeishuErrorItemMeta(ferr, nil),
				}); eerr != nil {
					return nil, eerr
				}
			} else {
				// Fetched, or an unsupported type (nothing to Fetch): record
				// the current edit time so the node is not re-processed next run.
				newTimes[resourceID][tok] = editTimeStr
				if len(items) > 0 {
					tally.Fetch()
					for _, it := range items {
						if eerr := h.Emit(ctx, *it); eerr != nil {
							return nil, eerr
						}
					}
				} else {
					// Unsupported type (mindnote/slides/…): no item.
					tally.Skip(ops.ObjType(node))
				}
			}

			processed++
			if processed%FeishuStreamCheckpointInterval == 0 || time.Since(lastCheckpoint) >= FeishuStreamCheckpointMaxInterval {
				if cerr := h.Checkpoint(ctx, ops.EncodeCursor(newTimes, lastSync)); cerr != nil {
					logger.Warnf(ctx, "%s stream checkpoint failed: %v", ops.LogTag(), cerr)
				}
				lastCheckpoint = time.Now()
			}
			if n := i + 1; n%100 == 0 {
				logger.Infof(ctx, "%s stream progress resource=%s %d/%d (%s)",
					ops.LogTag(), resourceID, n, len(nodes), tally.Summary())
			}
		}

		// Detect deleted nodes (only when the full tree was listed successfully).
		// A partial listing did not enumerate the whole subtree, so deletion
		// detection would false-positive.
		if partial == nil && prevTimes != nil {
			if prev, ok := prevTimes[resourceID]; ok {
				for tok := range prev {
					if !currentNodes[tok] {
						if eerr := h.Emit(ctx, types.FetchedItem{
							ExternalID:       tok,
							IsDeleted:        true,
							SourceResourceID: resourceID,
						}); eerr != nil {
							return nil, eerr
						}
					}
				}
			}
		}
		logger.Infof(ctx, "%s stream summary resource=%s %s", ops.LogTag(), resourceID, tally.Summary())
	}

	return ops.EncodeCursor(newTimes, lastSync), nil
}

// FetchStreamEngine runs the streaming sync. FetchStream / FetchIncremental
// shells differ only in whether they pass a cursor and how they collect
// results; both route here.
func FetchStreamEngine[N any](
	ctx context.Context, client *Client, config *types.DataSourceConfig,
	cursor *types.SyncCursor, h datasource.StreamHandler, ops NodeOps[N],
) (*types.SyncCursor, error) {
	return runSync(ctx, client, config, config.ResourceIDs, cursor, h, ops)
}

// FetchAllEngine runs a full sync, collecting every item into a slice. It
// passes the caller-supplied resourceIDs without a non-empty check (mirroring
// the original FetchAll which accepted an empty list and returned no items).
// On error any partially collected items are discarded, matching the original
// behaviour (a fatal list error dropped everything collected so far).
func FetchAllEngine[N any](
	ctx context.Context, client *Client, config *types.DataSourceConfig,
	resourceIDs []string, ops NodeOps[N],
) ([]types.FetchedItem, error) {
	ch := &CollectHandler{}
	if _, err := runSync(ctx, client, config, resourceIDs, nil, ch, ops); err != nil {
		return nil, err
	}
	return ch.items, nil
}

// FetchIncrementalEngine runs an incremental sync against cursor, collecting
// items. resourceIDs come from config.ResourceIDs (non-empty check is the
// caller's responsibility).
func FetchIncrementalEngine[N any](
	ctx context.Context, client *Client, config *types.DataSourceConfig,
	cursor *types.SyncCursor, ops NodeOps[N],
) ([]types.FetchedItem, *types.SyncCursor, error) {
	ch := &CollectHandler{}
	next, err := runSync(ctx, client, config, config.ResourceIDs, cursor, ch, ops)
	if err != nil {
		return nil, nil, err
	}
	return ch.items, next, nil
}
