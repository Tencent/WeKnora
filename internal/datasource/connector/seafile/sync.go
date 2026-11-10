package seafile

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// checkpointEvery bounds how many cursor mutations may be lost on a crash.
const checkpointEvery = 50

// syncRun holds the state of one FetchStream / FetchFullStream execution.
type syncRun struct {
	ctx    context.Context
	client *client
	h      datasource.StreamHandler
	cur    cursor
	repoID string
	// repoName is frozen in the cursor on the first run so folder names stay
	// stable when the library is renamed.
	repoName string
	limit    int64
	// seen records every supported file listed this run; anything known to
	// the cursor but not seen is a deletion candidate.
	seen    map[string]bool
	unsaved int
}

func (c *Connector) runSync(
	ctx context.Context, ds *types.DataSourceConfig, old *types.SyncCursor,
	h datasource.StreamHandler, forceFull bool,
) (*types.SyncCursor, error) {
	cfg, err := parseConfig(ds)
	if err != nil {
		return nil, err
	}
	repoID, roots, err := collapseRoots(ds.ResourceIDs)
	if err != nil {
		return nil, err
	}
	cur, changed, err := prepareCursor(old, repoID, forceFull)
	if err != nil {
		return nil, err
	}
	r := &syncRun{ctx: ctx, client: newClient(cfg), h: h, cur: cur, repoID: repoID, seen: make(map[string]bool)}
	if changed {
		r.unsaved++
	}

	// Nothing has been fetched yet, so failures here return without saving:
	// a cursor must not bind to a library that was never confirmed to exist.
	repos, err := r.client.listRepos(ctx)
	if err != nil {
		return nil, r.canceledOr(err)
	}
	repo, err := selectedRepo(repos, repoID)
	if err != nil {
		return nil, err
	}
	if r.cur.RepoName == "" {
		r.cur.RepoName = repo.Name
		r.unsaved++
	}
	r.repoName = r.cur.RepoName
	r.limit = utils.GetMaxFileSize()

	for _, root := range roots {
		if err := r.scanRoot(root); err != nil {
			return nil, err
		}
	}
	if err := r.reconcile(); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "[Seafile] sync finished: %d files in scope, %d tracked", len(r.seen), len(r.cur.Files))
	return r.cur.toSyncCursor(), nil
}

// canceledOr reports cancellation as ErrSyncCanceled and passes other errors
// through unchanged. The second branch covers a handler that failed on a
// context of its own; applying it twice is harmless.
func (r *syncRun) canceledOr(err error) error {
	if cause := r.ctx.Err(); cause != nil {
		return errors.Join(datasource.ErrSyncCanceled, cause)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(datasource.ErrSyncCanceled, err)
	}
	return err
}

func (r *syncRun) canceled() error {
	if cause := r.ctx.Err(); cause != nil {
		return errors.Join(datasource.ErrSyncCanceled, cause)
	}
	return nil
}

// abort saves unsaved progress while the context is still valid, then
// returns the source error. Handler errors never come through here.
func (r *syncRun) abort(err error) error {
	if canceled := r.canceledOr(err); canceled != err {
		return canceled
	}
	if r.unsaved > 0 {
		if checkpointErr := r.checkpoint(); checkpointErr != nil {
			return errors.Join(err, checkpointErr)
		}
	}
	return err
}

func (r *syncRun) checkpoint() error {
	if err := r.canceled(); err != nil {
		return err
	}
	if err := r.h.Checkpoint(r.ctx, r.cur.toSyncCursor()); err != nil {
		return err
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

func (r *syncRun) emit(item types.FetchedItem) error {
	if err := r.canceled(); err != nil {
		return err
	}
	if err := r.h.Emit(r.ctx, item); err != nil {
		return r.canceledOr(err)
	}
	return nil
}

func (r *syncRun) listDir(p string) ([]dirent, error) {
	if err := r.canceled(); err != nil {
		return nil, err
	}
	entries, err := r.client.listDir(r.ctx, r.repoID, p)
	if err != nil {
		return nil, r.canceledOr(err)
	}
	return entries, nil
}

// scanRoot classifies a selected root by its current type: a directory is
// traversed, a file listed by its parent is synced alone, anything else is
// gone.
func (r *syncRun) scanRoot(root string) error {
	entries, err := r.listDir(root)
	if err == nil {
		return r.walkDir(root, entries)
	}
	if statusOf(err) != http.StatusNotFound || root == "/" {
		return r.abort(mapDirError(err, root))
	}
	parent := parentPath(root)
	entries, err = r.listDir(parent)
	if err != nil {
		return r.abort(mapDirError(err, parent))
	}
	if err := validateListing(parent, entries); err != nil {
		return r.abort(err)
	}
	for _, e := range entries {
		if e.Name != path.Base(root) || e.Type != "file" {
			continue
		}
		if !utils.IsSupportedImportExtension(path.Ext(e.Name)) {
			return nil
		}
		return r.syncFile(root, e)
	}
	return r.abort(fmt.Errorf("%w: Seafile path %s", datasource.ErrResourceNotFound, root))
}

// validateListing rejects entries the traversal cannot trust; a bad listing
// is a directory failure so the run emits no deletions.
func validateListing(dir string, entries []dirent) error {
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		bad := !validEntryName(e.Name) || names[e.Name] || e.Mtime < 0 ||
			(e.Type != "file" && e.Type != "dir") ||
			(e.Type == "file" && (e.Size == nil || *e.Size < 0))
		if bad {
			return fmt.Errorf("%w: %w: directory listing %s", datasource.ErrFetchFailed, errInvalidResponse, dir)
		}
		names[e.Name] = true
	}
	return nil
}

// walkDir traverses root depth-first with an explicit stack of paths. Each
// level is listed once; files are synced before descending into
// sub-directories, both in name order.
func (r *syncRun) walkDir(root string, rootEntries []dirent) error {
	stack := []string{root}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries := rootEntries
		if dir != root {
			var err error
			if entries, err = r.listDir(dir); err != nil {
				// A sub-directory that vanished mid-scan is a listing failure,
				// not a missing selection: the run stops without deletions and
				// the next run sees the new shape.
				if statusOf(err) == http.StatusNotFound {
					return r.abort(fmt.Errorf("%w: Seafile directory %s disappeared during the scan",
						datasource.ErrFetchFailed, dir))
				}
				return r.abort(mapDirError(err, dir))
			}
		}
		if err := validateListing(dir, entries); err != nil {
			return r.abort(err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		for _, e := range entries {
			if e.Type == "file" && utils.IsSupportedImportExtension(path.Ext(e.Name)) {
				if err := r.syncFile(path.Join(dir, e.Name), e); err != nil {
					return err
				}
			}
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Type == "dir" {
				stack = append(stack, path.Join(dir, entries[i].Name))
			}
		}
	}
	return nil
}

// syncFile downloads and emits one file unless its fingerprint is unchanged.
// Every caller validated the listing, so e.Size is non-nil. The fingerprint
// is stored only after Emit succeeds.
func (r *syncRun) syncFile(p string, e dirent) error {
	if err := r.canceled(); err != nil {
		return err
	}
	if r.seen[p] {
		return nil
	}
	r.seen[p] = true
	size := *e.Size
	fp := fingerprint(e.ID, e.Mtime, size)
	if previous := r.cur.Files[p]; previous != "" && previous == fp {
		return nil
	}
	if size > r.limit {
		return r.failFile(p, e, reasonFileTooLarge, 0)
	}

	link, oid, err := r.client.downloadLink(r.ctx, r.repoID, p)
	if err != nil {
		return r.fetchError(p, e, err, linkReason(err))
	}
	if oid != "" && e.ID != "" && oid != e.ID {
		return r.failFile(p, e, reasonSourceChanged, 0)
	}
	body, err := r.client.download(r.ctx, link, size, r.limit)
	if err != nil {
		return r.fetchError(p, e, err, downloadReason(err))
	}
	if err := r.emit(r.buildItem(p, e, body)); err != nil {
		return err
	}
	r.cur.Files[p] = fp
	return r.mutated()
}

func linkReason(err error) string {
	switch {
	case statusOf(err) == http.StatusForbidden:
		return reasonPermissionDenied
	case statusOf(err) == http.StatusNotFound:
		return reasonNotFound
	case errors.Is(err, errInvalidResponse):
		return reasonInvalidResponse
	default:
		return reasonFetchFailed
	}
}

func downloadReason(err error) string {
	switch {
	case errors.Is(err, errSSRFBlocked):
		return reasonSSRFBlocked
	case errors.Is(err, errTooLarge):
		return reasonFileTooLarge
	case errors.Is(err, errEmptyFile):
		return reasonEmptyFile
	case errors.Is(err, errSourceChanged):
		return reasonSourceChanged
	default:
		return reasonFetchFailed
	}
}

// fetchError isolates a per-file failure as an error item, except for
// cancellation and an over-long Retry-After, which end the run. Client
// errors carry only the operation and status, so logging them is safe.
func (r *syncRun) fetchError(p string, e dirent, err error, code string) error {
	if canceled := r.canceledOr(err); canceled != err {
		return canceled
	}
	if errors.Is(err, errRetryAfterTooLong) {
		return r.abort(err)
	}
	logger.Warnf(r.ctx, "[Seafile] fetch failed: repo=%s path=%s code=%s: %v", r.repoID, p, code, err)
	return r.failFile(p, e, code, statusOf(err))
}

func (r *syncRun) failFile(p string, e dirent, code string, status int) error {
	if err := r.emit(r.errorItem(p, e, code, status)); err != nil {
		return err
	}
	return r.markFailed(p)
}

// markFailed keeps a known path alive with an empty fingerprint so it is
// retried next run and still reconciled if it disappears. Paths the cursor
// never knew stay absent.
func (r *syncRun) markFailed(p string) error {
	value, known := r.cur.Files[p]
	_, inBaseline := r.cur.FullSyncBaseline[p]
	if (known && value != "") || (!known && inBaseline) {
		r.cur.Files[p] = ""
		return r.mutated()
	}
	return nil
}

// reconcile emits deletions for every known path not seen this run. It only
// runs after every root enumerated successfully.
func (r *syncRun) reconcile() error {
	if err := r.canceled(); err != nil {
		return err
	}
	missing := make(map[string]bool)
	for p := range r.cur.Files {
		if !r.seen[p] {
			missing[p] = true
		}
	}
	if r.cur.FullSync {
		for p := range r.cur.FullSyncBaseline {
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
		if err := r.emit(r.deletedItem(p)); err != nil {
			return err
		}
		delete(r.cur.Files, p)
		delete(r.cur.FullSyncBaseline, p)
		if err := r.mutated(); err != nil {
			return err
		}
	}
	if r.cur.FullSync {
		r.cur.FullSync = false
		r.cur.FullSyncBaseline = nil
		r.unsaved++
	}
	if r.unsaved > 0 {
		return r.checkpoint()
	}
	return nil
}

func (r *syncRun) identity(p string, e dirent) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       externalID(r.repoID, p),
		Title:            r.repoName + "/" + strings.TrimPrefix(p, "/"),
		FileName:         knowledgeFileName(r.repoName, p),
		UpdatedAt:        time.Unix(e.Mtime, 0).UTC(),
		SourceResourceID: encodeResourceID(r.repoID, "/"),
	}
}

func (r *syncRun) buildItem(p string, e dirent, body []byte) types.FetchedItem {
	item := r.identity(p, e)
	item.Content = body
	item.ContentType = http.DetectContentType(body)
	item.Metadata = map[string]string{
		"channel":           types.ChannelSeafile,
		"source_type":       "seafile",
		"seafile_repo_id":   r.repoID,
		"seafile_repo_name": r.repoName,
		"seafile_path":      p,
		"seafile_object_id": e.ID,
		"seafile_mtime":     strconv.FormatInt(e.Mtime, 10),
		"seafile_size":      strconv.FormatInt(*e.Size, 10),
	}
	return item
}

// errorItem follows the service contract for failed fetches: no content, a
// stable reason code and English fallback text. The text is fixed so
// credentials and signed links never reach sync logs.
func (r *syncRun) errorItem(p string, e dirent, code string, status int) types.FetchedItem {
	item := r.identity(p, e)
	item.Metadata = map[string]string{
		"channel":           types.ChannelSeafile,
		"source_type":       "seafile",
		"seafile_path":      p,
		"error":             errorReason(code),
		"error_reason_code": code,
		"error_reason":      errorReason(code),
	}
	if status > 0 {
		item.Metadata["error_reason_code_value"] = strconv.Itoa(status)
	}
	return item
}

func (r *syncRun) deletedItem(p string) types.FetchedItem {
	return types.FetchedItem{
		ExternalID:       externalID(r.repoID, p),
		Title:            r.repoName + "/" + strings.TrimPrefix(p, "/"),
		IsDeleted:        true,
		SourceResourceID: encodeResourceID(r.repoID, "/"),
		Metadata:         map[string]string{"channel": types.ChannelSeafile, "seafile_path": p},
	}
}
