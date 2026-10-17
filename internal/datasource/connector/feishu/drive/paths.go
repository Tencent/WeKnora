package drive

import (
	"context"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// This file implements the P1 directory mapping for the Drive connector:
// FetchedItem.FileName becomes "<根文件夹名>/<子目录...>/<文件名>" rooted at
// the selected sync root's own display name (the root folder = a top-level KB
// folder) so ingestion's types.SplitKnowledgeRelativePath derives the KB
// folder_path. The recursive
// walk itself is core.WalkDriveTree (the Drive connector's single walker,
// shared with core): DFS order, shortcut expansion, partial-failure
// aggregation — plus a folder-token → cleaned directory path table built in
// the same pass, so no extra API calls are made. This file only resolves the
// per-resource root and feeds the walk results into Fetch.

// driveFolderName resolves a folder's display name via GetDriveFolderMeta,
// one call per token per sync run (cached on success only — a failure is not
// cached, so the next resource in the same run retries). resolved is false
// when the meta call failed or returned no name — that folder's path prefix
// segment (root or sub-folder) is then skipped.
func (o *driveOps) driveFolderName(
	ctx context.Context, client *core.Client, folderToken string,
) (name string, resolved bool) {
	if o.folderNames == nil {
		o.folderNames = make(map[string]string)
	}
	if name, ok := o.folderNames[folderToken]; ok {
		return name, true
	}
	meta, err := client.GetDriveFolderMeta(ctx, folderToken)
	if err != nil {
		logger.Warnf(ctx, "[FeishuDrive] resolve folder name for %s: %v (falling back)", folderToken, err)
		return "", false
	}
	if meta.Data.Name == "" {
		return "", false
	}
	name = core.SanitizeFileName(meta.Data.Name)
	o.folderNames[folderToken] = name
	return name, true
}

// listDriveFilesForResource lists the files to sync for a resourceID and
// records the directory-path table for Fetch. A resourceID is either a bare
// rootFolderToken (sync the whole subtree) or "rootFolderToken:fileToken"
// (sync a single selected file or sub-folder). Every walk is rooted at the
// root folder's own display name, so the synced folder appears as a top-level
// KB folder holding its subtree (an unresolved root name falls back to no
// prefix, mirroring pre-root-mirror behaviour).
func (o *driveOps) listDriveFilesForResource(
	ctx context.Context, client *core.Client, resourceID string,
) ([]core.DriveFile, error) {
	rootFolderToken, fileToken := parseDriveResourceID(resourceID)

	rootBase := ""
	if rootName, ok := o.driveFolderName(ctx, client, rootFolderToken); ok {
		rootBase = rootName
	}

	if fileToken == "" {
		// The selected root folder maps to a top-level KB folder named after
		// it: documents directly under it land in folder_path "<根名>".
		files, dirPaths, failures := core.WalkDriveTree(ctx, client, rootFolderToken, rootBase)
		o.dirPaths = dirPaths
		return files, partialDriveError(failures)
	}

	// A shallow root listing identifies direct files without probing them with
	// folder APIs, and direct folders without visiting unselected siblings.
	if o.rootEntries == nil {
		o.rootEntries = make(map[string][]core.DriveFile)
	}
	entries, cached := o.rootEntries[rootFolderToken]
	if !cached {
		if listed, err := client.ListDriveFilesAllPages(ctx, rootFolderToken); err == nil {
			entries = listed
			o.rootEntries[rootFolderToken] = listed
		}
	}
	base := rootBase
	folderFound := false
	for _, entry := range entries {
		f, ok := core.ExpandDriveShortcut(entry)
		if !ok || (entry.Token != fileToken && f.Token != fileToken) {
			continue
		}
		if f.Type != "folder" {
			// Some list responses omit parent_token; the listing itself tells
			// us the containing directory.
			f.ParentToken = rootFolderToken
			o.dirPaths = map[string]string{rootFolderToken: rootBase}
			return []core.DriveFile{f}, nil
		}
		folderFound = true
		base = joinDriveDir(rootBase, core.SanitizeFileName(f.Name))
		break
	}
	if !folderFound {
		// A nested selected folder can be walked directly, even when the
		// caller cannot list its parent. No full-root discovery is needed.
		if subName, ok := o.driveFolderName(ctx, client, fileToken); ok {
			base = joinDriveDir(rootBase, subName)
		}
	}
	files, dirPaths, failures := core.WalkDriveTree(ctx, client, fileToken, base)
	for _, failure := range failures {
		if failure.FolderToken == fileToken && isDriveNotFolderError(failure.Err) {
			// Only nested single files need the legacy root search: Drive
			// exposes no single-file parent query. Never use this fallback for
			// permission/transport failures, which must retain the old cursor.
			rootFiles, rootDirs, rootFailures := core.WalkDriveTree(ctx, client, rootFolderToken, rootBase)
			o.dirPaths = rootDirs
			return filterDriveFileByToken(rootFiles, fileToken), partialDriveError(rootFailures)
		}
	}
	o.dirPaths = dirPaths
	return files, partialDriveError(failures)
}

func joinDriveDir(parent, name string) string {
	if parent == "" {
		return name
	}
	if name == "" {
		return parent
	}
	return parent + "/" + name
}

// partialDriveError wraps collected failures into a
// *core.PartialDriveFileListError (nil when there are none).
func partialDriveError(failures []core.DriveFileListFailure) error {
	if len(failures) == 0 {
		return nil
	}
	return &core.PartialDriveFileListError{Failures: failures}
}

// qualifyItemFileNames prefixes every fetched item's FileName with dir so
// ingestion derives the KB folder path. The prefix applies to the main item
// and to attachment/image sub-items alike — they all live in the same folder.
// An empty dir (parent folder not in the current walk) or FileName is untouched.
func qualifyItemFileNames(items []*types.FetchedItem, dir string) {
	if dir == "" {
		return
	}
	for _, it := range items {
		if it != nil && it.FileName != "" {
			it.FileName = dir + "/" + it.FileName
		}
	}
}
