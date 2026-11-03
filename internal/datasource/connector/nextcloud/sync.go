package nextcloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	checkpointEvery     = 50
	maxTraversalEntries = 1000000
	maxTraversalDepth   = 256
)

type syncRun struct {
	ctx              context.Context
	client           *client
	cfg              config
	h                datasource.StreamHandler
	cur              cursor
	seen             map[string]bool
	limit            int64
	unsaved, scanned int
}

func (*Connector) runSync(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor, h datasource.StreamHandler, full bool,
) (*types.SyncCursor, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	roots, err := collapseRoots(ds.ResourceIDs)
	if err != nil {
		return nil, err
	}
	cur, err := prepareCursor(old, cfg.scope(), full)
	if err != nil {
		return nil, err
	}
	r := &syncRun{
		ctx: ctx, client: newClient(cfg), cfg: cfg, h: h, cur: cur,
		seen: make(map[string]bool), limit: utils.GetMaxFileSize(), unsaved: 1,
	}
	defer r.client.http.CloseIdleConnections()
	for _, root := range roots {
		if err := r.scanRoot(root); err != nil {
			return nil, err
		}
	}
	if err := r.reconcile(); err != nil {
		return nil, err
	}
	return r.cur.toSyncCursor(), nil
}

func (r *syncRun) canceledOr(err error) error {
	if r.ctx.Err() != nil {
		return errors.Join(datasource.ErrSyncCanceled, r.ctx.Err())
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(datasource.ErrSyncCanceled, err)
	}
	return err
}

func (r *syncRun) checkpoint() error {
	if err := r.canceledOr(nil); err != nil {
		return err
	}
	if err := r.h.Checkpoint(r.ctx, r.cur.toSyncCursor()); err != nil {
		return r.canceledOr(err)
	}
	r.unsaved = 0
	return nil
}

func (r *syncRun) mutated() error {
	r.unsaved++
	if r.unsaved >= checkpointEvery {
		return r.checkpoint()
	}
	return nil
}

// Source failures save successfully emitted progress. Handler failures return
// directly and never advance the cursor past the unsuccessful operation.
func (r *syncRun) abort(err error) error {
	if canceled := r.canceledOr(err); errors.Is(canceled, datasource.ErrSyncCanceled) {
		return canceled
	}
	if r.unsaved > 0 {
		if checkpointErr := r.checkpoint(); checkpointErr != nil {
			return errors.Join(err, checkpointErr)
		}
	}
	return err
}

func (r *syncRun) emit(item types.FetchedItem) error {
	if err := r.canceledOr(nil); err != nil {
		return err
	}
	return r.canceledOr(r.h.Emit(r.ctx, item))
}

func mapListingError(err error) error {
	switch statusOf(err) {
	case http.StatusNotFound:
		return fmt.Errorf("%w: selected Nextcloud resource was not found", datasource.ErrResourceNotFound)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", datasource.ErrFetchFailed, reasonPermissionDenied)
	default:
		return err
	}
}

func (r *syncRun) scanRoot(root string) error {
	if err := r.canceledOr(nil); err != nil {
		return err
	}
	entries, err := r.client.propfind(r.ctx, root, 0)
	if err != nil {
		return r.abort(mapListingError(err))
	}
	if !entries[0].IsDir {
		if r.cfg.includes(root) {
			return r.syncFile(entries[0])
		}
		return nil
	}
	stack := []string{root}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if strings.Count(dir, "/") > maxTraversalDepth {
			return r.abort(invalidResponse())
		}
		entries, err := r.client.propfind(r.ctx, dir, 1)
		if err != nil {
			return r.abort(mapListingError(err))
		}
		r.scanned += len(entries)
		if r.scanned > maxTraversalEntries {
			return r.abort(invalidResponse())
		}
		for _, e := range entries {
			if e.Path == dir {
				if !e.IsDir {
					return r.abort(invalidResponse())
				}
				continue
			}
			if !e.IsDir && r.cfg.includes(e.Path) {
				if err := r.syncFile(e); err != nil {
					return err
				}
			}
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].IsDir && entries[i].Path != dir {
				stack = append(stack, entries[i].Path)
			}
		}
	}
	return nil
}

func (r *syncRun) syncFile(e entry) error {
	if err := r.canceledOr(nil); err != nil {
		return err
	}
	if r.seen[e.Path] {
		return nil
	}
	r.seen[e.Path] = true
	fp := fingerprint(e)
	if r.cur.Files[e.Path] == fp {
		return nil
	}
	body, err := r.client.download(r.ctx, e, r.limit)
	if err != nil {
		fatal := errors.Is(err, datasource.ErrInvalidCredentials) ||
			errors.Is(r.canceledOr(err), datasource.ErrSyncCanceled)
		if fatal {
			return r.abort(err)
		}
		item := r.identity(e)
		code := fetchReason(err)
		item.Metadata["error"] = reasonText[code]
		item.Metadata["error_reason"] = reasonText[code]
		item.Metadata["error_reason_code"] = code
		if status := statusOf(err); status > 0 {
			item.Metadata["error_reason_code_value"] = strconv.Itoa(status)
		}
		if err := r.emit(item); err != nil {
			return err
		}
		_, known := r.cur.Files[e.Path]
		_, baseline := r.cur.FullSyncBaseline[e.Path]
		if known || baseline {
			r.cur.Files[e.Path] = ""
			return r.mutated()
		}
		return nil
	}
	item := r.identity(e)
	item.Content, item.ContentType = body, http.DetectContentType(body)
	if err := r.emit(item); err != nil {
		return err
	}
	r.cur.Files[e.Path] = fp
	return r.mutated()
}

func fetchReason(err error) string {
	switch {
	case errors.Is(err, errSSRFBlocked):
		return reasonSSRFBlocked
	case errors.Is(err, errTooLarge):
		return reasonFileTooLarge
	case errors.Is(err, errEmptyFile):
		return reasonEmptyFile
	case errors.Is(err, errSourceChanged):
		return reasonSourceChanged
	case errors.Is(err, errInvalidResponse):
		return reasonInvalidResponse
	case statusOf(err) == http.StatusForbidden:
		return reasonPermissionDenied
	case statusOf(err) == http.StatusNotFound:
		return reasonNotFound
	default:
		return reasonFetchFailed
	}
}

func (r *syncRun) identity(e entry) types.FetchedItem {
	return types.FetchedItem{
		ExternalID: externalID(r.cur.Scope, e.Path),
		Title:      "Nextcloud" + e.Path, FileName: knowledgeFileName(e.Path), UpdatedAt: e.ModifiedAt,
		SourceResourceID: "/", Metadata: map[string]string{
			"channel": types.ChannelNextcloud, "source_type": "nextcloud", "nextcloud_path": e.Path,
			"nextcloud_file_id": e.FileID, "nextcloud_etag": e.ETag, "nextcloud_size": strconv.FormatInt(e.Size, 10),
		},
	}
}

// Only a complete traversal can establish absence. Persist additions before
// emitting any deletions, then checkpoint each batch of acknowledged removals.
func (r *syncRun) reconcile() error {
	missing := map[string]bool{}
	for _, files := range []map[string]string{r.cur.Files, r.cur.FullSyncBaseline} {
		for p := range files {
			if !r.seen[p] {
				missing[p] = true
			}
		}
	}
	paths := make([]string, 0, len(missing))
	for p := range missing {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) > 0 && r.unsaved > 0 {
		if err := r.checkpoint(); err != nil {
			return err
		}
	}
	for _, p := range paths {
		item := r.identity(entry{Path: p})
		item.IsDeleted = true
		if err := r.emit(item); err != nil {
			return err
		}
		delete(r.cur.Files, p)
		delete(r.cur.FullSyncBaseline, p)
		if err := r.mutated(); err != nil {
			return err
		}
	}
	r.cur.FullSync, r.cur.FullSyncBaseline = false, nil
	return r.checkpoint()
}
