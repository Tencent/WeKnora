package dingtalk

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

const (
	resourceIDPrefix   = "dingtalk:v1?"
	maxResourceIDBytes = 8 * 1024
	maxResourceDepth   = 64
)

// describedResourcePrefix is the picker-only parentID form that asks
// ListResources to describe ONE reference instead of listing its children.
//
// It exists because a saved manual selection can never be listed: nothing
// enumerates Bases or personal-space nodes, so the picker would otherwise have
// nothing but the id to show for it. The same reference also has to stay
// expandable — "list the children of this Base" and "tell me what this Base is
// called" are two different questions — so the describe request is a distinct
// form rather than extra rows smuggled into the children call, which would make
// parentID mean something other than "the parent whose children are wanted".
//
// The value is an encoded resource reference, so the describe form inherits the
// same validation; it is never stored in a selection or a cursor.
const describedResourcePrefix = resourceIDPrefix + "describe="

// describedResourceParentID renders the parentID that asks for a description of
// one reference.
func describedResourceParentID(reference string) string {
	return describedResourcePrefix + reference
}

// resourceReference is embedded in resource picker IDs for folders and
// documents. Workspace IDs remain unchanged for compatibility with data
// sources created by earlier builds of the connector.
//
// WorkspaceID may be empty when NodeID is set: the user can paste a node or
// Base id that no picker can offer — a personal-space document
// (GET /v2.0/wiki/workspaces lists team workspaces only) or a
// multi-dimensional table (nothing lists Bases) — and a workspace id is not
// something a user can know. The connector then adopts the workspace the node
// itself reports, so a bare "dingtalk:v1?node=<id>" resolves end to end.
type resourceReference struct {
	WorkspaceID string
	NodeID      string
	Ancestors   []string

	// BaseID addresses a multi-dimensional table (able). A Base is a wiki node
	// — getNode resolves it and listNodes lists the documents underneath — but
	// not a wiki document: its tables are read through the notable API by the
	// Base's own id, and no listing API enumerates Bases, so the id has to come
	// from the user. The workspace is adopted from the node the Base reports,
	// so BaseID is never combined with the fields above.
	BaseID string
}

func (r resourceReference) child(nodeID string) resourceReference {
	ancestors := append([]string(nil), r.Ancestors...)
	if r.NodeID != "" {
		ancestors = append(ancestors, r.NodeID)
	}
	return resourceReference{
		WorkspaceID: r.WorkspaceID,
		NodeID:      strings.TrimSpace(nodeID),
		Ancestors:   ancestors,
	}
}

func encodeResourceReference(ref resourceReference) (string, error) {
	if err := validateResourceReference(ref); err != nil {
		return "", err
	}
	if ref.BaseID != "" {
		return encodeResourceQuery(url.Values{"base": {ref.BaseID}})
	}
	if ref.NodeID == "" {
		return ref.WorkspaceID, nil
	}

	values := url.Values{}
	// The workspace is omitted rather than sent empty for the bare node form:
	// an empty parameter would be an invalid id, while an absent one means
	// "resolve the workspace from the node itself".
	if ref.WorkspaceID != "" {
		values.Set("workspace", ref.WorkspaceID)
	}
	values.Set("node", ref.NodeID)
	for _, ancestor := range ref.Ancestors {
		values.Add("ancestor", ancestor)
	}
	return encodeResourceQuery(values)
}

// encodeResourceQuery renders the query part of a resource reference, keeping
// the length guard in one place.
func encodeResourceQuery(values url.Values) (string, error) {
	encoded := resourceIDPrefix + values.Encode()
	if len(encoded) > maxResourceIDBytes {
		return "", fmt.Errorf("DingTalk resource ID exceeds %d bytes", maxResourceIDBytes)
	}
	return encoded, nil
}

func decodeResourceReference(raw string) (resourceReference, error) {
	if len(raw) == 0 || len(raw) > maxResourceIDBytes {
		return resourceReference{}, fmt.Errorf("invalid DingTalk resource ID")
	}
	if !strings.HasPrefix(raw, resourceIDPrefix) {
		ref := resourceReference{WorkspaceID: raw}
		if err := validateResourceReference(ref); err != nil {
			return resourceReference{}, err
		}
		return ref, nil
	}

	values, err := url.ParseQuery(strings.TrimPrefix(raw, resourceIDPrefix))
	if err != nil {
		return resourceReference{}, fmt.Errorf("parse DingTalk resource ID: %w", err)
	}
	for key := range values {
		if key != "workspace" && key != "node" && key != "ancestor" && key != "base" {
			return resourceReference{}, fmt.Errorf("DingTalk resource ID contains an unsupported field")
		}
	}
	if len(values["base"]) > 1 {
		return resourceReference{}, fmt.Errorf("DingTalk resource ID has an invalid base field")
	}
	if len(values["base"]) == 1 {
		// A Base names no workspace and no node, so the other fields must be
		// absent rather than merely ignored.
		if len(values["workspace"]) != 0 || len(values["node"]) != 0 || len(values["ancestor"]) != 0 {
			return resourceReference{}, fmt.Errorf(
				"DingTalk base resource cannot name a workspace, node or ancestors")
		}
		ref := resourceReference{BaseID: values.Get("base")}
		if err := validateResourceReference(ref); err != nil {
			return resourceReference{}, err
		}
		return ref, nil
	}
	// The workspace field is optional: "dingtalk:v1?node=<id>" is the manual
	// entry form, accepted because a personal-space node is readable by id even
	// though no workspace listing returns it. The node itself then reports the
	// workspace (see resolveSyncScopes).
	if len(values["workspace"]) > 1 || len(values["node"]) != 1 {
		return resourceReference{}, fmt.Errorf("DingTalk resource ID has invalid workspace or node fields")
	}
	ref := resourceReference{
		WorkspaceID: values.Get("workspace"),
		NodeID:      values.Get("node"),
		Ancestors:   append([]string(nil), values["ancestor"]...),
	}
	if err := validateResourceReference(ref); err != nil {
		return resourceReference{}, err
	}
	return ref, nil
}

func validateResourceReference(ref resourceReference) error {
	if ref.BaseID != "" {
		if err := validateExternalID("base", ref.BaseID); err != nil {
			return err
		}
		// A Base is a whole object of its own: it has no workspace, no node and
		// no path, so mixing the forms would describe something that cannot be
		// fetched.
		if ref.WorkspaceID != "" || ref.NodeID != "" || len(ref.Ancestors) != 0 {
			return fmt.Errorf("DingTalk base resource cannot name a workspace, node or ancestors")
		}
		return nil
	}
	// A wiki reference names at least one thing. It used to require a workspace
	// unconditionally; the workspace is now optional when a node is named,
	// because a personal-space node cannot be enumerated but is resolvable by id
	// through getNode. This is a deliberate widening of the wire format: every
	// previously valid reference still validates and encodes unchanged, and the
	// message for the still-invalid "neither field" case is kept as the
	// workspace one it has always been.
	if ref.WorkspaceID == "" && ref.NodeID == "" {
		return fmt.Errorf("DingTalk resource has invalid workspace ID")
	}
	if ref.WorkspaceID != "" {
		if err := validateExternalID("workspace", ref.WorkspaceID); err != nil {
			return err
		}
	}
	if ref.NodeID != "" {
		if err := validateExternalID("node", ref.NodeID); err != nil {
			return err
		}
	} else if len(ref.Ancestors) != 0 {
		return fmt.Errorf("DingTalk workspace resource cannot have ancestors")
	}
	if len(ref.Ancestors) != 0 && ref.WorkspaceID == "" {
		// Ancestors describe a path from a workspace root. Without a workspace
		// there is no root to walk from, so the path could be neither validated
		// nor followed.
		return fmt.Errorf("DingTalk node resource without a workspace cannot have ancestors")
	}
	if len(ref.Ancestors) > maxResourceDepth {
		return fmt.Errorf("DingTalk resource depth exceeds %d", maxResourceDepth)
	}

	seen := make(map[string]struct{}, len(ref.Ancestors)+1)
	for _, ancestor := range ref.Ancestors {
		if err := validateExternalID("ancestor", ancestor); err != nil {
			return err
		}
		if _, exists := seen[ancestor]; exists {
			return fmt.Errorf("DingTalk resource contains an ancestor cycle")
		}
		seen[ancestor] = struct{}{}
	}
	if _, exists := seen[ref.NodeID]; ref.NodeID != "" && exists {
		return fmt.Errorf("DingTalk resource contains a node cycle")
	}
	return nil
}

func validateExternalID(kind, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("DingTalk resource has invalid %s ID", kind)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("DingTalk resource has invalid %s ID", kind)
		}
	}
	return nil
}

func resourceAncestorIDs(ref resourceReference) ([]string, error) {
	root := resourceReference{WorkspaceID: ref.WorkspaceID}
	if ref.BaseID != "" {
		// A Base is its own root: the reference is the whole path.
		root = resourceReference{BaseID: ref.BaseID}
	}
	if root.WorkspaceID == "" && root.BaseID == "" {
		// A bare node reference names no workspace, so there is no root to walk
		// from: the node sits in a personal space no listing exposes. Returning
		// no ancestors is what keeps the picker from trying to expand a node it
		// can never show.
		return nil, nil
	}
	rootID, err := encodeResourceReference(root)
	if err != nil {
		return nil, err
	}
	ancestors := []string{rootID}
	for index, nodeID := range ref.Ancestors {
		id, err := encodeResourceReference(resourceReference{
			WorkspaceID: ref.WorkspaceID,
			NodeID:      nodeID,
			Ancestors:   append([]string(nil), ref.Ancestors[:index]...),
		})
		if err != nil {
			return nil, err
		}
		ancestors = append(ancestors, id)
	}
	return ancestors, nil
}
