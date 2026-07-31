package service

import (
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// --- Sentinel errors for the document-folder service ---
//
// These are mapped to HTTP status codes by the handler
// (writeDocumentFolderError). Keep names stable — the handler error-maps by
// identity (errors.Is), not by message text.
//
// NotFound is intentionally NOT redefined here: we reuse the repository's
// repository.ErrDocumentFolderNotFound (the wiki-page service follows the
// same pattern with repository.ErrWikiFolderNotFound) so errors.Is chains
// work uniformly across the repo / service / handler boundary.

var (
	// ErrFolderConflict — HTTP 409. A sibling folder with the same name
	// already exists under the same parent.
	ErrFolderConflict = errors.New("document folder name conflict")

	// ErrFolderNotEmpty — HTTP 409. Tried to delete a folder whose subtree
	// still contains child folders or filed documents.
	ErrFolderNotEmpty = errors.New("document folder is not empty")

	// ErrFolderDepthExceeded — HTTP 400. Depth would exceed MaxFolderDepth or
	// the materialized path would exceed MaxFolderPathLen.
	ErrFolderDepthExceeded = errors.New("document folder depth or path length exceeded")

	// ErrFolderNameInvalid — HTTP 400. Name is blank, contains path
	// separators, or is otherwise illegal.
	ErrFolderNameInvalid = errors.New("document folder name is invalid")

	// ErrFolderLimitExceeded — HTTP 400. Folder count for the KB would exceed
	// MaxFoldersPerKB, or a subtree expansion exceeds the same cap.
	ErrFolderLimitExceeded = errors.New("document folder limit exceeded")

	// ErrFolderCycleInData — HTTP 500. Cycle detected while walking a folder
	// tree that is supposed to be acyclic — indicates data corruption.
	ErrFolderCycleInData = errors.New("document folder cycle detected in stored data")

	// ErrFolderCursorInvalid — HTTP 400. A folder-list cursor must be the
	// opaque keyset position emitted by the previous response.
	ErrFolderCursorInvalid = errors.New("document folder cursor is invalid")

	// ErrFolderDocumentsProcessing means keep-documents deletion cannot safely
	// proceed because a parser may still write the old folder payload.
	ErrFolderDocumentsProcessing = errors.New("documents in the folder are still being parsed")

	// ErrFolderChangedDuringDelete asks the background task to retry after a
	// concurrent upload or folder mutation changed the planned subtree.
	ErrFolderChangedDuringDelete = errors.New("document folder changed during deletion")
)

// isRepoNotFound reports whether err is the repository's not-found sentinel
// (repository.ErrDocumentFolderNotFound). Centralized so the service code does
// not import the repository package at every call site.
func isRepoNotFound(err error) bool {
	return errors.Is(err, repository.ErrDocumentFolderNotFound)
}

// validateDocumentFolderName trims the name and rejects blank /
// separator-bearing names. Path separators are refused so the materialized
// path stays unambiguous — the path uses "/" as the joiner, so a name
// containing "/" would corrupt the hierarchy display and the LIKE prefix
// match used for materialized-path maintenance. Mirrors the wiki folder
// validator (wiki_page.go: validateFolderName) but lives in its own name space
// to avoid a collision.
func validateDocumentFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrFolderNameInvalid
	}
	if len(name) > types.MaxFolderNameLen {
		return "", ErrFolderNameInvalid
	}
	// Refuse forward + fullwidth + halfwidth slashes — any character used as
	// a path separator in the materialized path cache.
	if strings.ContainsAny(name, "/｜／|\\") {
		return "", ErrFolderNameInvalid
	}
	return name, nil
}

// resolveSubtreeFolderIDs expands the folder subtree rooted at rootID via BFS
// over an adjacency list, returning rootID + every descendant. This is the
// L3 query-time expansion: the caller passes the already-loaded ListAllFolders
// result (one DB round-trip per KB) and gets back the folder-ID set to feed
// into a vector-store terms/IN filter.
//
// The expansion is bounded by MaxFoldersPerKB: when a subtree itself exceeds
// the cap (which only happens when the KB is at the limit and the query
// targets the root), we fail closed rather than silently truncating.
//
// Cycle guard: a folder reached twice means the stored data is corrupt; we
// return ErrFolderCycleInData rather than spin.
func resolveSubtreeFolderIDs(all []*types.DocumentFolder, rootID string) ([]string, error) {
	// Locate the root and verify it's live.
	var root *types.DocumentFolder
	for _, f := range all {
		if f.ID == rootID {
			root = f
			break
		}
	}
	if root == nil {
		return nil, repository.ErrDocumentFolderNotFound
	}

	// children[parentID] = list of child folder IDs. Built once, walked by BFS.
	children := make(map[string][]string, len(all))
	for _, f := range all {
		if f.ParentID != "" {
			children[f.ParentID] = append(children[f.ParentID], f.ID)
		}
	}

	visited := map[string]bool{rootID: true}
	result := []string{rootID}
	queue := []string{rootID}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range children[cur] {
			if visited[child] {
				// Reaching a node twice can only happen if the parent_id graph
				// contains a cycle — a data-integrity bug. Fail loud.
				return nil, ErrFolderCycleInData
			}
			visited[child] = true
			result = append(result, child)
			queue = append(queue, child)
		}
	}

	if len(result) > types.MaxFoldersPerKB {
		return nil, ErrFolderLimitExceeded
	}
	return result, nil
}
