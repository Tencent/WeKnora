package dingtalk

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// describedNodeURL is the address a reader can open a described node at in
// DingTalk. It repeats the fallback the connector already applies when it
// fetches a document, because a describe row and a fetched item are the same
// node seen by two different callers.
func describedNodeURL(described node) string {
	if location := strings.TrimSpace(described.URL); location != "" {
		return location
	}
	return "https://alidocs.dingtalk.com/i/nodes/" + url.PathEscape(described.ID)
}

// describeResource answers the picker's "describe this reference" request with
// the one row that names what the reference selects.
//
// The picker reaches it because a manual selection is invisible to every
// listing: nothing enumerates Bases, and GET /v2.0/wiki/workspaces lists team
// workspaces only, so a personal-space node is absent from the tree as well.
// Without a description the preview of such a selection can only print the id
// the user pasted.
//
// The row describes the reference itself and never a child of it: expanding the
// same reference is a separate question with a separate answer (see
// ListResources), and a row returned as a child would be placed under itself by
// the picker's lazy tree. The requested reference is echoed as ExternalID so the
// picker can match the row to the selection it asked about.
//
// Only a context error fails the call. A reference whose name cannot be read
// still describes itself with its id: a lookup the picker makes to improve a
// label must never turn a selection the sync can read into a broken step.
func describeResource(ctx context.Context, api dingTalkAPI, rawReference string) (types.Resource, error) {
	ref, err := decodeResourceReference(rawReference)
	if err != nil {
		return types.Resource{}, err
	}
	// Describe the canonical form: it is the id the selection stores, so the
	// picker matches the row against resource_ids without normalizing anything.
	canonicalID, err := encodeResourceReference(ref)
	if err != nil {
		return types.Resource{}, err
	}
	switch {
	case ref.BaseID != "":
		return describeBase(ctx, api, ref.BaseID, canonicalID)
	case ref.NodeID != "":
		return describeNode(ctx, api, ref.NodeID, canonicalID)
	default:
		// A workspace is a row of the root listing already; naming it would
		// need the listing this path deliberately avoids.
		return types.Resource{}, fmt.Errorf(
			"%w: DingTalk resource %q is neither a node nor a Base",
			datasource.ErrResourceNotFound, canonicalID)
	}
}

// describeNode names one referenced wiki node. The node is read by id, exactly
// as the sync reads it, so a personal-space node resolves without any workspace
// listing and the picker's name cannot disagree with the item the sync writes.
func describeNode(
	ctx context.Context,
	api dingTalkAPI,
	nodeID, canonicalID string,
) (types.Resource, error) {
	resource := types.Resource{
		ExternalID: canonicalID,
		Name:       nodeID,
		Metadata:   map[string]interface{}{"node_id": nodeID},
	}
	described, err := api.getNode(ctx, nodeID)
	if err != nil {
		if isContextError(err) {
			return types.Resource{}, err
		}
		logger.Debugf(ctx,
			"[DingTalk] describe node %s: %v; the picker shows the id instead",
			nodeID, err)
		return resource, nil
	}
	if name := strings.TrimSpace(described.Name); name != "" {
		resource.Name = name
	}
	resource.Type = describedResourceType(described)
	resource.URL = describedNodeURL(described)
	resource.ModifiedAt = described.modifiedAt()
	// Only a folder is expandable through the picker: a node reference the
	// connector cannot list children for must not offer an expander that can
	// only fail.
	resource.HasChildren = described.isFolder()
	if workspaceID := strings.TrimSpace(described.WorkspaceID); workspaceID != "" {
		resource.Metadata["workspace_id"] = workspaceID
	}
	if described.Category != "" {
		resource.Metadata["category"] = described.Category
	}
	if described.Extension != "" {
		resource.Metadata["extension"] = described.Extension
	}
	return resource, nil
}

// describeBase names one multi-dimensional table (Base).
//
// A Base needs no workspace: it is addressed by its own id, and the wiki-nodes
// API answers for it — a Base is a node in the wiki tree — which is where both
// the name and the children come from. The Base's tables are listed as well,
// because they, not the Base's wiki children, are what a base= reference syncs:
// the row then says what selecting it ingests instead of leaving a reader to
// assume the documents hanging under it are that content.
func describeBase(
	ctx context.Context,
	api dingTalkAPI,
	baseID, canonicalID string,
) (types.Resource, error) {
	resource := types.Resource{
		ExternalID: canonicalID,
		Name:       baseID,
		Type:       resourceTypeBase,
		// Expandability is whatever the wiki API reports when it answered at
		// all: a Base with no wiki children has no expander to offer. The
		// default stays true for a Base the wiki API could not describe — the
		// children are listed through the id the reference carries, quite
		// independently of the name, so a failed lookup is no evidence of
		// having none.
		HasChildren: true,
		Metadata: map[string]interface{}{
			"base_id":   baseID,
			"extension": "able",
			"category":  "ALIDOC",
		},
	}
	if described, err := api.getNode(ctx, baseID); err != nil {
		if isContextError(err) {
			return types.Resource{}, err
		}
		// A tenant whose wiki API does not know the Base still syncs it through
		// the notable API, so the id stands in for the missing name.
		logger.Debugf(ctx,
			"[DingTalk] describe base %s: %v; the picker shows the id instead",
			baseID, err)
	} else {
		if name := strings.TrimSpace(described.Name); name != "" {
			resource.Name = name
		}
		resource.URL = describedNodeURL(described)
		resource.ModifiedAt = described.modifiedAt()
		resource.HasChildren = described.HasChildren
		if nodeID := strings.TrimSpace(described.ID); nodeID != "" {
			resource.Metadata["node_id"] = nodeID
		}
		if workspaceID := strings.TrimSpace(described.WorkspaceID); workspaceID != "" {
			resource.Metadata["workspace_id"] = workspaceID
		}
	}
	tables, err := api.listNotableTables(ctx, baseID)
	if err != nil {
		if isContextError(err) {
			return types.Resource{}, err
		}
		// The row keeps its name: naming the tables is an explanation, not a
		// prerequisite for describing the selection.
		logger.Debugf(ctx,
			"[DingTalk] describe base %s: table listing failed: %v", baseID, err)
		return resource, nil
	}
	resource.Description = notableTableNames(tables)
	return resource, nil
}

// notableTableNames joins the tables of one Base in listing order, which is the
// order the rendered document uses. An unnamed table contributes nothing rather
// than a placeholder: the row is a name, not a table inventory.
func notableTableNames(tables []notableTable) string {
	names := make([]string, 0, len(tables))
	for _, table := range tables {
		if name := strings.TrimSpace(table.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

// describedResourceType is the picker type of a wiki node. Folders and
// documents carry the same two words the workspace listing uses; anything
// without an ingest path gets no type at all, so the picker shows no label
// rather than promising a sync the connector cannot perform.
func describedResourceType(described node) string {
	switch {
	case described.isFolder():
		return "folder"
	case described.isDocument():
		return "document"
	default:
		return ""
	}
}
