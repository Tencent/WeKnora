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

// resourceReference is embedded in resource picker IDs for folders and
// documents. Workspace IDs remain unchanged for compatibility with data
// sources created by earlier builds of the connector.
type resourceReference struct {
	WorkspaceID string
	NodeID      string
	Ancestors   []string
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
	if ref.NodeID == "" {
		return ref.WorkspaceID, nil
	}

	values := url.Values{}
	values.Set("workspace", ref.WorkspaceID)
	values.Set("node", ref.NodeID)
	for _, ancestor := range ref.Ancestors {
		values.Add("ancestor", ancestor)
	}
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
		if key != "workspace" && key != "node" && key != "ancestor" {
			return resourceReference{}, fmt.Errorf("DingTalk resource ID contains an unsupported field")
		}
	}
	if len(values["workspace"]) != 1 || len(values["node"]) != 1 {
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
	if err := validateExternalID("workspace", ref.WorkspaceID); err != nil {
		return err
	}
	if ref.NodeID != "" {
		if err := validateExternalID("node", ref.NodeID); err != nil {
			return err
		}
	} else if len(ref.Ancestors) != 0 {
		return fmt.Errorf("DingTalk workspace resource cannot have ancestors")
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
	rootID, err := encodeResourceReference(resourceReference{WorkspaceID: ref.WorkspaceID})
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
