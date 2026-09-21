package drive

import (
	"context"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// This file implements the P1 directory mapping for the Drive connector:
// FetchedItem.FileName becomes "<子目录...>/<文件名>" relative to the selected
// sync root (the root folder = the knowledge base root) so ingestion's
// types.SplitKnowledgeRelativePath derives the KB folder_path. The recursive
// walk itself is core.WalkDriveTree (the Drive connector's single walker,
// shared with core): DFS order, shortcut expansion, partial-failure
// aggregation — plus a folder-token → cleaned directory path table built in
// the same pass, so no extra API calls are made. This file only resolves the
// per-resource root and feeds the walk results into Fetch.

// driveFolderName resolves a folder's display name via GetDriveFolderMeta,
// one call per token per sync run (cached). resolved is false when the meta
// call failed or returned no name — the sub-folder prefix is then skipped.
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
// root folderToken (sync the whole subtree) or "rootFolderToken:fileToken"
// (sync a single selected file or sub-folder).
func (o *driveOps) listDriveFilesForResource(
	ctx context.Context, client *core.Client, resourceID string,
) ([]core.DriveFile, error) {
	rootFolderToken, fileToken := parseDriveResourceID(resourceID)

	if fileToken == "" {
		// The selected root folder maps to the knowledge base root: documents
		// directly under it land in folder_path "".
		files, dirPaths, failures := core.WalkDriveTree(ctx, client, rootFolderToken, "")
		o.dirPaths = dirPaths
		return files, partialDriveError(failures)
	}

	// Sub-selection ("rootFolderToken:fileToken"). The selected node's kind is
	// read off the root subtree listing — probing it with folder APIs would
	// fail for file tokens (folder meta 91202, folder list 1061002) and log
	// API errors on every sync.
	rootFiles, rootDirs, rootFailures := core.WalkDriveTree(ctx, client, rootFolderToken, "")
	for _, f := range rootFiles {
		if f.Token == fileToken && f.Type != "folder" {
			o.dirPaths = rootDirs
			return filterDriveFileByToken(rootFiles, fileToken), partialDriveError(rootFailures)
		}
	}

	// Sub-folder selection: walk that sub-folder's subtree, prefixed with the
	// sub-folder's own name (relative to the KB root; unresolved name → the
	// walk runs with no prefix). If the token turns out to be a file missing
	// from the root listing (partial listing, permission edge), fall back to
	// the already-fetched root subtree — mirroring single-file behaviour.
	base := ""
	if subName, ok := o.driveFolderName(ctx, client, fileToken); ok {
		base = subName
	}
	files, dirPaths, failures := core.WalkDriveTree(ctx, client, fileToken, base)
	for _, failure := range failures {
		if failure.FolderToken == fileToken && isDriveNotFolderError(failure.Err) {
			o.dirPaths = rootDirs
			return filterDriveFileByToken(rootFiles, fileToken), partialDriveError(rootFailures)
		}
	}
	o.dirPaths = dirPaths
	return files, partialDriveError(failures)
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
