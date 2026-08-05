package dingtalk

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// syncState is the connector-specific cursor. DocRevisions maps a node ID to the
// revision token last successfully ingested, which is what makes an incremental
// run able to skip unchanged documents.
type syncState struct {
	LastSyncTime        time.Time         `json:"last_sync_time"`
	IdentityFingerprint string            `json:"identity_fingerprint,omitempty"`
	DocRevisions        map[string]string `json:"doc_revisions,omitempty"`
	// DocMetadataHashes detects title/source metadata changes even when
	// DingTalk leaves the content modification timestamp unchanged.
	DocMetadataHashes map[string]string `json:"doc_metadata_hashes,omitempty"`
	// EmptySnapshotCount is retained as an aggregate compatibility field for
	// cursors written before deletion confirmation became resource-scoped.
	EmptySnapshotCount int `json:"empty_snapshot_count,omitempty"`
	// Resources records which resource IDs the cursor covers, so a later run can
	// tell "not seen because it is gone" apart from "not seen because this
	// resource was not synced".
	Resources []string `json:"resources,omitempty"`
	// Scopes keeps deletion evidence independent for every selected resource.
	// A healthy empty response from one workspace must not be hidden by another
	// workspace still returning documents.
	Scopes map[string]scopeSyncState `json:"scopes,omitempty"`
}

type scopeSyncState struct {
	DocRevisions       map[string]string `json:"doc_revisions,omitempty"`
	DocMetadataHashes  map[string]string `json:"doc_metadata_hashes,omitempty"`
	EmptySnapshotCount int               `json:"empty_snapshot_count,omitempty"`
}

// walkResult accumulates one traversal.
type walkResult struct {
	items []types.FetchedItem
	// visited prevents cycles and duplicate downloads when selected resources
	// overlap (for example, a workspace and a document inside it).
	visited map[string]struct{}
	// seen holds nodes observed this run, mapped to their revision.
	seen           map[string]string
	metadataHashes map[string]string
	// scopeSeen and scopeComplete retain evidence per selected resource. This is
	// the safety boundary for deletion inference.
	scopeSeen           map[string]map[string]string
	scopeMetadataHashes map[string]map[string]string
	scopeComplete       map[string]bool
	// warnings are recoverable conditions that must be visible to operators.
	warnings []string
	// fatal stops the traversal for credentials, permissions, invalid config,
	// and context cancellation.
	fatal error
}

const maxPartialFetchDetails = 20
const legacyScopeKey = "\x00legacy-global-snapshot"
const previousIdentityScopeKey = "\x00previous-identity-cleanup"

func (r *walkResult) addWarning(detail string) {
	if detail == "" || len(r.warnings) >= maxPartialFetchDetails {
		return
	}
	r.warnings = append(r.warnings, detail)
}

func (r *walkResult) ensureScope(resourceID string) map[string]string {
	seen, ok := r.scopeSeen[resourceID]
	if !ok {
		seen = make(map[string]string)
		r.scopeSeen[resourceID] = seen
		r.scopeComplete[resourceID] = true
	}
	return seen
}

func (r *walkResult) recordSeen(resourceID, nodeID, revision string) {
	r.recordSeenWithMetadata(resourceID, nodeID, revision, "")
}

func (r *walkResult) ensureMetadataScope(resourceID string) map[string]string {
	hashes, ok := r.scopeMetadataHashes[resourceID]
	if !ok {
		hashes = make(map[string]string)
		r.scopeMetadataHashes[resourceID] = hashes
	}
	return hashes
}

func (r *walkResult) recordSeenWithMetadata(
	resourceID string,
	nodeID string,
	revision string,
	metadataHash string,
) {
	r.seen[nodeID] = revision
	r.ensureScope(resourceID)[nodeID] = revision
	if metadataHash != "" {
		r.metadataHashes[nodeID] = metadataHash
		r.ensureMetadataScope(resourceID)[nodeID] = metadataHash
	}
}

func (r *walkResult) markIncomplete(resourceID string) {
	r.ensureScope(resourceID)
	r.scopeComplete[resourceID] = false
}

func (r *walkResult) allScopesComplete(resourceIDs []string) bool {
	for _, resourceID := range resourceIDs {
		if !r.scopeComplete[resourceID] {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func cloneRevisions(revisions map[string]string) map[string]string {
	cloned := make(map[string]string, len(revisions))
	for id, revision := range revisions {
		cloned[id] = revision
	}
	return cloned
}

func carryRevisions(current, previous map[string]string) int {
	carried := 0
	for id, revision := range previous {
		if _, alreadySeen := current[id]; alreadySeen {
			continue
		}
		current[id] = revision
		carried++
	}
	return carried
}

func previousScopeStates(prev *syncState, currentResources []string) map[string]scopeSyncState {
	scopes := make(map[string]scopeSyncState)
	if prev == nil {
		return scopes
	}
	for resourceID, scope := range prev.Scopes {
		scopes[resourceID] = scopeSyncState{
			DocRevisions:       cloneRevisions(scope.DocRevisions),
			DocMetadataHashes:  cloneRevisions(scope.DocMetadataHashes),
			EmptySnapshotCount: scope.EmptySnapshotCount,
		}
	}
	if len(scopes) > 0 || len(prev.DocRevisions) == 0 || len(currentResources) != 1 {
		return scopes
	}

	resourceID := currentResources[0]
	if len(prev.Resources) == 1 && prev.Resources[0] != "" {
		resourceID = prev.Resources[0]
	}
	scopes[resourceID] = scopeSyncState{
		DocRevisions:       cloneRevisions(prev.DocRevisions),
		DocMetadataHashes:  cloneRevisions(prev.DocMetadataHashes),
		EmptySnapshotCount: prev.EmptySnapshotCount,
	}
	return scopes
}

func rebuildAggregateCursor(state *syncState) {
	state.DocRevisions = make(map[string]string)
	state.DocMetadataHashes = make(map[string]string)
	state.EmptySnapshotCount = 0
	for _, scope := range state.Scopes {
		for id, revision := range scope.DocRevisions {
			state.DocRevisions[id] = revision
		}
		for id, metadataHash := range scope.DocMetadataHashes {
			state.DocMetadataHashes[id] = metadataHash
		}
		if scope.EmptySnapshotCount > state.EmptySnapshotCount {
			state.EmptySnapshotCount = scope.EmptySnapshotCount
		}
	}
}

// walk traverses the selected resources, fetching document content.
//
// D1 (failure-safe deletion): a listing error inside the tree degrades the run
// instead of failing it, and marks the traversal incomplete. Deletions are then
// suppressed for that run, and every previously-known document is carried
// forward in the cursor so no knowledge is silently dropped.
func (c *Connector) walk(
	ctx context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	prev *syncState,
	detectDeletions bool,
	skipUnchanged bool,
) ([]types.FetchedItem, *syncState, error) {
	cfg, err := parseConfig(config)
	if err != nil {
		return nil, nil, err
	}
	cli, err := c.makeClient(cfg)
	if err != nil {
		return nil, nil, err
	}
	identityFingerprint := cfg.cursorIdentityFingerprint()
	if prev != nil && prev.IdentityFingerprint != identityFingerprint {
		// Never reuse revisions across external identities. Retain the previous
		// identity only as a cleanup-only scope: once every replacement scope is
		// observed completely, owned rows that exist only in the old identity
		// are emitted as deletions. Incomplete replacement snapshots carry this
		// scope forward instead of orphaning old content.
		logger.Infof(ctx, "[DingTalk] incremental cursor identity changed; starting a fresh baseline with deferred cleanup")
		staleRevisions := cloneRevisions(prev.DocRevisions)
		staleMetadataHashes := cloneRevisions(prev.DocMetadataHashes)
		if len(staleRevisions) == 0 {
			prev = nil
		} else {
			prev = &syncState{
				IdentityFingerprint: identityFingerprint,
				Scopes: map[string]scopeSyncState{
					previousIdentityScopeKey: {
						DocRevisions:      staleRevisions,
						DocMetadataHashes: staleMetadataHashes,
					},
				},
			}
		}
	}

	resourceIDs = uniqueStrings(resourceIDs)
	res := &walkResult{
		seen:                make(map[string]string),
		metadataHashes:      make(map[string]string),
		visited:             make(map[string]struct{}),
		scopeSeen:           make(map[string]map[string]string),
		scopeMetadataHashes: make(map[string]map[string]string),
		scopeComplete:       make(map[string]bool),
	}

	for _, rid := range resourceIDs {
		res.ensureScope(rid)
		nodeID, workspaceID, selected, err := c.resolveSelection(ctx, cli, rid)
		if err != nil {
			if errors.Is(err, errSelectedResourceMissing) {
				// The encoded parent path was listed successfully and no
				// longer contains this selection. Leave the scope complete
				// and empty so the normal two-snapshot confirmation policy
				// can safely emit its previous documents as deletions.
				logger.Infof(ctx, "[DingTalk] selected resource is absent from its verified parent path")
				continue
			}
			// A resource we cannot even resolve is treated as unreachable, not
			// as an empty resource: fail closed on deletions.
			if isFatal(err) {
				return nil, nil, err
			}
			logger.Warnf(ctx, "[DingTalk] resource %s unresolvable, skipping: %v",
				redactIdentifier(rid), err)
			res.markIncomplete(rid)
			res.addWarning("a selected DingTalk resource could not be resolved; deletion was suppressed")
			continue
		}
		if selected == nil {
			c.walkNode(ctx, cli, workspaceID, nodeID, rid, 0, prev, skipUnchanged, res)
			if res.fatal != nil {
				break
			}
			continue
		}

		handled := false
		supportedDocument := selected.isSupportedDocument()
		if supportedDocument {
			handled = true
			c.fetchDocument(ctx, cli, selected, rid, prev, skipUnchanged, res)
			if res.fatal != nil {
				break
			}
		}
		if selected.canDescend() {
			handled = true
			if !supportedDocument {
				preserveClassificationDrift(prev, rid, selected.NodeID, res)
			}
			c.walkNode(ctx, cli, workspaceID, nodeID, rid, 0, prev, skipUnchanged, res)
		}
		if res.fatal != nil {
			break
		}
		if !handled {
			logger.Warnf(ctx, "[DingTalk] selected node %s is not a supported online document",
				redactIdentifier(nodeID))
			res.markIncomplete(rid)
			res.addWarning("a selected DingTalk node is not a supported online document")
		}
	}

	if res.fatal != nil {
		return nil, nil, res.fatal
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	next := &syncState{
		LastSyncTime:        time.Now(),
		IdentityFingerprint: identityFingerprint,
		Resources:           resourceIDs,
		Scopes:              make(map[string]scopeSyncState, len(resourceIDs)),
	}
	for _, resourceID := range resourceIDs {
		next.Scopes[resourceID] = scopeSyncState{
			DocRevisions:      cloneRevisions(res.ensureScope(resourceID)),
			DocMetadataHashes: cloneRevisions(res.ensureMetadataScope(resourceID)),
		}
	}

	if !detectDeletions {
		rebuildAggregateCursor(next)
		if len(res.warnings) > 0 {
			return res.items, next, &datasource.PartialFetchError{Details: res.warnings}
		}
		return res.items, next, nil
	}

	deletionCandidates := make(map[string]struct{})
	previousScopes := previousScopeStates(prev, resourceIDs)
	currentResources := make(map[string]struct{}, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		currentResources[resourceID] = struct{}{}
		currentScope := next.Scopes[resourceID]
		previousScope, existed := previousScopes[resourceID]
		if !existed {
			continue
		}

		if !res.scopeComplete[resourceID] {
			carried := carryRevisions(currentScope.DocRevisions, previousScope.DocRevisions)
			carryRevisions(currentScope.DocMetadataHashes, previousScope.DocMetadataHashes)
			// Empty deletion evidence is only meaningful when observations are
			// consecutive and complete. Any incomplete traversal breaks that
			// chain, even though the previous document revisions are retained.
			currentScope.EmptySnapshotCount = 0
			next.Scopes[resourceID] = currentScope
			logger.Warnf(ctx, "[DingTalk] incomplete resource listing: suppressed deletion "+
				"detection and carried %d previously-known documents forward", carried)
			continue
		}

		if len(previousScope.DocRevisions) > 0 && len(currentScope.DocRevisions) == 0 {
			if previousScope.EmptySnapshotCount == 0 {
				carryRevisions(currentScope.DocRevisions, previousScope.DocRevisions)
				carryRevisions(currentScope.DocMetadataHashes, previousScope.DocMetadataHashes)
				currentScope.EmptySnapshotCount = 1
				next.Scopes[resourceID] = currentScope
				res.addWarning("DingTalk returned an unexpectedly empty resource snapshot; deletions were suppressed")
				continue
			}
			for id := range previousScope.DocRevisions {
				deletionCandidates[id] = struct{}{}
			}
			continue
		}

		for id := range previousScope.DocRevisions {
			if _, stillThere := currentScope.DocRevisions[id]; !stillThere {
				deletionCandidates[id] = struct{}{}
			}
		}
	}

	for resourceID, previousScope := range previousScopes {
		if _, stillSelected := currentResources[resourceID]; stillSelected {
			continue
		}
		// A resource-selection change is only safe to reconcile against a
		// complete snapshot of every newly selected scope. Otherwise a failed
		// credential/resource switch could delete all content from the previous
		// selection before the replacement was ever observed successfully.
		if !res.allScopesComplete(resourceIDs) {
			next.Scopes[resourceID] = previousScope
			continue
		}
		for id := range previousScope.DocRevisions {
			deletionCandidates[id] = struct{}{}
		}
	}

	if prev != nil && len(prev.Scopes) == 0 && len(prev.DocRevisions) > 0 && len(resourceIDs) > 1 {
		// A legacy global cursor cannot prove which resource owned each document.
		// Carry it for one fully observed baseline before reconciling it.
		next.Scopes[legacyScopeKey] = scopeSyncState{
			DocRevisions:       cloneRevisions(prev.DocRevisions),
			DocMetadataHashes:  cloneRevisions(prev.DocMetadataHashes),
			EmptySnapshotCount: prev.EmptySnapshotCount + 1,
		}
		res.addWarning("DingTalk upgraded deletion tracking to per-resource snapshots; legacy deletions were suppressed for this run")
	}

	rebuildAggregateCursor(next)
	var deletedIDs []string
	for id := range deletionCandidates {
		if _, stillPresent := next.DocRevisions[id]; !stillPresent {
			deletedIDs = append(deletedIDs, id)
		}
	}
	sort.Strings(deletedIDs)
	for _, id := range deletedIDs {
		res.items = append(res.items, types.FetchedItem{
			ExternalID: id,
			IsDeleted:  true,
			Metadata: map[string]string{
				"channel": types.ConnectorTypeDingTalk,
			},
		})
	}

	if len(res.warnings) > 0 {
		return res.items, next, &datasource.PartialFetchError{Details: res.warnings}
	}
	return res.items, next, nil
}

// preserveClassificationDrift keeps the last successful document revision
// when the current node payload no longer proves that the node is a supported
// online document. The scope is incomplete because this ambiguity cannot serve
// as confirmed deletion evidence.
func preserveClassificationDrift(
	prev *syncState,
	resourceID string,
	nodeID string,
	res *walkResult,
) {
	visitKey := "classification-drift:" + resourceID + ":" + nodeID
	if _, alreadyPreserved := res.visited[visitKey]; alreadyPreserved {
		return
	}
	prevRev, ok := previousResourceRevision(prev, resourceID, nodeID)
	if !ok {
		return
	}
	res.visited[visitKey] = struct{}{}
	res.recordSeen(resourceID, nodeID, prevRev)
	res.markIncomplete(resourceID)
	res.addWarning("a previously imported DingTalk node no longer has a supported online-document classification; deletion was suppressed")
}

// walkNode recursively processes a container node, appending fetched documents.
// Errors on individual nodes degrade the run rather than aborting it.
func (c *Connector) walkNode(
	ctx context.Context,
	cli *client,
	workspaceID, nodeID, resourceID string,
	depth int,
	prev *syncState,
	incremental bool,
	res *walkResult,
) {
	// Include the selected resource in the cycle key. Overlapping selections
	// still share document downloads below, but each scope must independently
	// observe its descendants for safe deletion reconciliation.
	visitKey := "container:" + resourceID + ":" + workspaceID + ":" + nodeID
	if _, ok := res.visited[visitKey]; ok {
		return
	}
	res.visited[visitKey] = struct{}{}

	if depth > maxDepth {
		logger.Warnf(ctx, "[DingTalk] max depth %d reached at node %s, not descending",
			maxDepth, redactIdentifier(nodeID))
		res.markIncomplete(resourceID)
		res.addWarning("DingTalk directory depth exceeded the safety limit; the subtree was skipped")
		return
	}

	children, err := cli.listAllChildren(ctx, workspaceID, nodeID)
	if err != nil {
		if isFatal(err) {
			res.fatal = err
			return
		}
		logger.Warnf(ctx, "[DingTalk] list children of %s failed, subtree skipped: %v",
			redactIdentifier(nodeID), err)
		res.markIncomplete(resourceID)
		res.addWarning("a DingTalk directory could not be listed; deletion was suppressed for the incomplete snapshot")
		return
	}

	for i := range children {
		if ctx.Err() != nil {
			res.fatal = ctx.Err()
			return
		}
		n := &children[i]
		if n.WorkspaceID != "" && n.WorkspaceID != workspaceID {
			logger.Warnf(ctx, "[DingTalk] node %s was returned for an unexpected workspace, skipping",
				redactIdentifier(n.NodeID))
			res.markIncomplete(resourceID)
			res.addWarning("DingTalk returned a node from a different workspace; the node was skipped and deletion was suppressed")
			continue
		}
		if n.WorkspaceID == "" {
			n.WorkspaceID = workspaceID
		}
		handled := false
		supportedDocument := n.isSupportedDocument()
		if supportedDocument {
			handled = true
			c.fetchDocument(ctx, cli, n, resourceID, prev, incremental, res)
		}
		if res.fatal != nil {
			return
		}
		if n.canDescend() {
			handled = true
			// A document may temporarily lose its category/key hints while
			// still advertising children. Continue walking the subtree, but
			// preserve a previously imported parent as ambiguous instead of
			// treating it as a confirmed deletion.
			if !supportedDocument {
				preserveClassificationDrift(prev, resourceID, n.NodeID, res)
			}
			c.walkNode(ctx, cli, workspaceID, n.NodeID, resourceID, depth+1, prev, incremental, res)
		}
		if !handled {
			visitKey := "unsupported:" + resourceID + ":" + n.NodeID
			if _, ok := res.visited[visitKey]; ok {
				continue
			}
			res.visited[visitKey] = struct{}{}
			// Unsupported leaves are not documents and must never become ghost
			// entries in the document revision cursor. If a previously imported
			// document now classifies as unsupported, preserve its last known
			// revision and make the traversal incomplete so it cannot be
			// mistaken for a confirmed deletion.
			if prevRev, ok := previousResourceRevision(prev, resourceID, n.NodeID); ok {
				res.recordSeen(resourceID, n.NodeID, prevRev)
				res.markIncomplete(resourceID)
				res.addWarning("a previously imported DingTalk node no longer has a supported online-document classification; deletion was suppressed")
			}
			logger.Debugf(ctx, "[DingTalk] skip unsupported node %s (type=%q)",
				redactIdentifier(n.NodeID), n.Type)
		}
		if res.fatal != nil {
			return
		}
	}
}

// fetchDocument downloads and converts one document, honoring the incremental
// revision check.
func (c *Connector) fetchDocument(
	ctx context.Context,
	cli *client,
	n *node,
	resourceID string,
	prev *syncState,
	incremental bool,
	res *walkResult,
) {
	visitKey := "document:" + n.NodeID
	if _, ok := res.visited[visitKey]; ok {
		if revision, observed := res.seen[n.NodeID]; observed {
			res.ensureScope(resourceID)[n.NodeID] = revision
		}
		if metadataHash, observed := res.metadataHashes[n.NodeID]; observed {
			res.ensureMetadataScope(resourceID)[n.NodeID] = metadataHash
		}
		return
	}
	res.visited[visitKey] = struct{}{}

	rev := n.revision()
	metadataHash := n.metadataRevision()

	if incremental {
		if prevRev, ok := previousResourceRevision(prev, resourceID, n.NodeID); ok &&
			prevRev == rev && rev != "" {
			previousMetadataHash, metadataKnown := previousResourceMetadataHash(
				prev,
				resourceID,
				n.NodeID,
			)
			// A legacy cursor has no metadata hash. Seed it without forcing a
			// one-time download of every unchanged document during upgrade.
			if !metadataKnown || previousMetadataHash == metadataHash {
				res.recordSeenWithMetadata(resourceID, n.NodeID, rev, metadataHash)
				return
			}
		}
	}

	blocks, err := cli.listDocumentBlocks(ctx, n.documentKey())
	if err != nil {
		if isFatal(err) {
			res.fatal = err
			return
		}
		// The node exists — record it so it is never mistaken for a deletion —
		// but do not advance its revision, so the next run retries the content.
		res.recordSeen(resourceID, n.NodeID, "")
		if prevRev, ok := previousResourceRevision(prev, resourceID, n.NodeID); ok {
			previousMetadataHash, _ := previousResourceMetadataHash(
				prev,
				resourceID,
				n.NodeID,
			)
			res.recordSeenWithMetadata(
				resourceID,
				n.NodeID,
				prevRev,
				previousMetadataHash,
			)
		}
		res.markIncomplete(resourceID)
		logger.Warnf(ctx, "[DingTalk] fetch document %s failed: %v",
			redactIdentifier(n.NodeID), err)
		res.addWarning("a DingTalk online document could not be read and will be retried")
		return
	}

	markdown, unknownBlockKinds := blocksToMarkdownWithDiagnostics(blocks)
	if len(unknownBlockKinds) > 0 {
		for _, kind := range unknownBlockKinds {
			logger.Warnf(
				ctx,
				"[DingTalk] document %s used unsupported block type %q; nested content was preserved",
				redactIdentifier(n.NodeID),
				boundedDiagnosticValue(kind),
			)
		}
		res.addWarning(fmt.Sprintf(
			"a DingTalk document used %d unsupported block type(s); nested content was preserved",
			len(unknownBlockKinds),
		))
	}
	if strings.TrimSpace(markdown) == "" {
		title := strings.TrimSpace(n.Name)
		if title == "" {
			title = "Untitled DingTalk document"
		}
		markdown = "# " + title + "\n"
	}
	res.recordSeenWithMetadata(resourceID, n.NodeID, rev, metadataHash)
	res.items = append(res.items, types.FetchedItem{
		ExternalID:       n.NodeID,
		Title:            n.Name,
		Content:          []byte(markdown),
		ContentType:      "text/markdown",
		FileName:         sanitizeFileName(n.Name) + ".md",
		URL:              safeSourceURL(n.URL),
		UpdatedAt:        n.lastModified(),
		SourceResourceID: resourceID,
		Metadata: map[string]string{
			"channel":      types.ConnectorTypeDingTalk,
			"node_id":      n.NodeID,
			"workspace_id": n.WorkspaceID,
			"doc_key":      n.DocKey,
			"revision":     rev,
		},
	})
}

func boundedDiagnosticValue(value string) string {
	value = strings.TrimSpace(value)
	const maxRunes = 64
	runes := []rune(value)
	if len(runes) > maxRunes {
		value = string(runes[:maxRunes]) + "..."
	}
	return value
}

func previousResourceRevision(
	prev *syncState,
	resourceID string,
	nodeID string,
) (string, bool) {
	if prev == nil {
		return "", false
	}
	if len(prev.Scopes) > 0 {
		scope, ok := prev.Scopes[resourceID]
		if !ok {
			return "", false
		}
		revision, found := scope.DocRevisions[nodeID]
		return revision, found
	}
	revision, found := prev.DocRevisions[nodeID]
	return revision, found
}

func previousResourceMetadataHash(
	prev *syncState,
	resourceID string,
	nodeID string,
) (string, bool) {
	if prev == nil {
		return "", false
	}
	if len(prev.Scopes) > 0 {
		scope, ok := prev.Scopes[resourceID]
		if !ok {
			return "", false
		}
		metadataHash, found := scope.DocMetadataHashes[nodeID]
		return metadataHash, found
	}
	metadataHash, found := prev.DocMetadataHashes[nodeID]
	return metadataHash, found
}

// safeSourceURL keeps a stable document link while dropping query parameters,
// fragments, and user information that may contain short-lived signatures or
// private access material.
func safeSourceURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

// isFatal reports whether an error should abort the whole sync rather than
// degrade it. Credential and config problems will not fix themselves by
// continuing, and continuing past them would produce a spuriously empty listing.
func isFatal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, datasource.ErrInvalidCredentials) ||
		errors.Is(err, datasource.ErrInvalidConfig) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 401 || apiErr.StatusCode == 403
	}
	return false
}

func isNotFoundAPIError(err error) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 404
}
