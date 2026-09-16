package drive

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// This file implements the P1 directory mapping for the Drive connector:
// FetchedItem.FileName becomes "<根文件夹名>/<子目录...>/<文件名>" so ingestion's
// types.SplitKnowledgeRelativePath derives the KB folder_path. The walk is the
// Drive connector's single recursive walker (it absorbed the former
// core.ListDriveFilesRecursiveFrom, which could not hand back folder names):
// DFS order, shortcut expansion,
// partial-failure aggregation — plus a folder-token → cleaned directory path
// table built in the same pass, so no extra API calls are made.

// walkDriveTree walks the folder subtree depth-first. Contract:
//   - folder -> recurse
//   - shortcut -> expand to its target (target_type is never "folder")
//   - other -> collect
//
// Listing failures are collected into the returned failures and the walk
// continues (PartialDriveFileListError semantics). dirPaths maps every visited
// folder token to its cleaned directory path; the walk root's own prefix is
// baseDir (the root folder name, optionally plus the selected sub-folder).
func walkDriveTree(
	ctx context.Context, client *core.Client, rootToken, baseDir string,
) (files []core.DriveFile, dirPaths map[string]string, failures []core.DriveFileListFailure) {
	dirPaths = map[string]string{rootToken: baseDir}
	visited := make(map[string]bool)

	var walk func(folderToken string)
	walk = func(folderToken string) {
		if visited[folderToken] {
			return
		}
		visited[folderToken] = true

		entries, err := client.ListDriveFilesAllPages(ctx, folderToken)
		if err != nil {
			wrappedErr := fmt.Errorf("list children of %s: %w", folderToken, err)
			failures = append(failures, core.DriveFileListFailure{
				FolderToken: folderToken,
				Err:         wrappedErr,
			})
			logger.Warnf(ctx, "[FeishuDrive] partial drive file listing failure: folder=%s err=%v",
				folderToken, err)
			return
		}

		for _, f := range entries {
			switch f.Type {
			case "folder":
				dirPaths[f.Token] = dirPaths[folderToken] + "/" + core.SanitizeFileName(f.Name)
				walk(f.Token)
			case "shortcut":
				// Expand to target. target_type is never "folder" (verified), so
				// no recursion here - the target is a regular file.
				if f.ShortcutInfo != nil && f.ShortcutInfo.TargetToken != "" {
					files = append(files, core.DriveFile{
						Token:        f.ShortcutInfo.TargetToken,
						Name:         f.Name,
						Type:         f.ShortcutInfo.TargetType,
						ParentToken:  f.ParentToken,
						URL:          f.URL,
						CreatedTime:  f.CreatedTime,
						ModifiedTime: f.ModifiedTime,
						OwnerID:      f.OwnerID,
					})
				}
			default:
				files = append(files, f)
			}
		}
	}

	walk(rootToken)
	return files, dirPaths, failures
}

// driveFolderName resolves a folder's display name via GetDriveFolderMeta,
// one call per token per sync run (cached). resolved is false when the meta
// call failed or returned no name — callers fall back (root: the token itself,
// mirroring ListResources; sub-folder: skip the segment).
func (o *driveOps) driveFolderName(ctx context.Context, client *core.Client, folderToken string) (name string, resolved bool) {
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
	rootName, ok := o.driveFolderName(ctx, client, rootFolderToken)
	if !ok {
		rootName = core.SanitizeFileName(rootFolderToken)
	}

	if fileToken == "" {
		files, dirPaths, failures := walkDriveTree(ctx, client, rootFolderToken, rootName)
		o.dirPaths = dirPaths
		return files, partialDriveError(failures)
	}

	// Sub-folder selection: walk that sub-folder's subtree, prefixed
	// "<rootName>/<subName>". If the token turns out to be a file (the walk
	// fails with the params error), fall back to walking the root subtree and
	// filtering — mirroring the previous single-file selection behaviour.
	base := rootName
	if subName, ok := o.driveFolderName(ctx, client, fileToken); ok {
		base = rootName + "/" + subName
	}
	files, dirPaths, failures := walkDriveTree(ctx, client, fileToken, base)
	for _, failure := range failures {
		if failure.FolderToken == fileToken && isDriveNotFolderError(failure.Err) {
			rootFiles, rootDirs, rootFailures := walkDriveTree(ctx, client, rootFolderToken, rootName)
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
