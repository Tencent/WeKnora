package dingtalk

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	resourceIDPrefix   = "dingtalk:v1:"
	maxResourceIDBytes = 8192
	maxResourceDepth   = 64
)

// resourceRef is deliberately opaque to the frontend. Ancestors contains node
// ids from the workspace root's first visible child down to the direct parent.
// Keeping the lineage in the saved resource id lets the lazy picker reveal a
// nested selection without scanning an entire DingTalk workspace.
type resourceRef struct {
	WorkspaceID string   `json:"w"`
	NodeID      string   `json:"n,omitempty"`
	Ancestors   []string `json:"a,omitempty"`
}

func workspaceResourceRef(workspaceID string) resourceRef {
	return resourceRef{WorkspaceID: strings.TrimSpace(workspaceID)}
}

func childResourceRef(parent resourceRef, nodeID string) resourceRef {
	ancestors := append([]string(nil), parent.Ancestors...)
	if parent.NodeID != "" {
		ancestors = append(ancestors, parent.NodeID)
	}
	return resourceRef{
		WorkspaceID: parent.WorkspaceID,
		NodeID:      strings.TrimSpace(nodeID),
		Ancestors:   ancestors,
	}
}

func encodeResourceRef(ref resourceRef) (string, error) {
	if err := validateResourceRef(ref); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(ref)
	if err != nil {
		return "", fmt.Errorf("marshal dingtalk resource id: %w", err)
	}
	if len(encoded) > maxResourceIDBytes {
		return "", fmt.Errorf("dingtalk resource id exceeds %d bytes", maxResourceIDBytes)
	}
	return resourceIDPrefix + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeResourceRef(resourceID string) (resourceRef, error) {
	resourceID = strings.TrimSpace(resourceID)
	if !strings.HasPrefix(resourceID, resourceIDPrefix) {
		return resourceRef{}, fmt.Errorf("invalid dingtalk resource id prefix")
	}
	payload := strings.TrimPrefix(resourceID, resourceIDPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return resourceRef{}, fmt.Errorf("decode dingtalk resource id: %w", err)
	}
	if len(decoded) == 0 || len(decoded) > maxResourceIDBytes {
		return resourceRef{}, fmt.Errorf("invalid dingtalk resource id length")
	}
	var ref resourceRef
	if err := json.Unmarshal(decoded, &ref); err != nil {
		return resourceRef{}, fmt.Errorf("parse dingtalk resource id: %w", err)
	}
	if err := validateResourceRef(ref); err != nil {
		return resourceRef{}, err
	}
	return ref, nil
}

func validateResourceRef(ref resourceRef) error {
	workspaceID := strings.TrimSpace(ref.WorkspaceID)
	if workspaceID == "" {
		return fmt.Errorf("dingtalk resource id has empty workspace id")
	}
	if workspaceID != ref.WorkspaceID {
		return fmt.Errorf("dingtalk resource id has non-canonical workspace id")
	}
	if ref.NodeID != strings.TrimSpace(ref.NodeID) {
		return fmt.Errorf("dingtalk resource id has non-canonical node id")
	}
	if len(ref.Ancestors) > maxResourceDepth {
		return fmt.Errorf("dingtalk resource depth exceeds %d", maxResourceDepth)
	}
	if ref.NodeID == "" && len(ref.Ancestors) != 0 {
		return fmt.Errorf("workspace resource cannot contain ancestors")
	}
	seen := make(map[string]struct{}, len(ref.Ancestors)+1)
	for _, ancestor := range ref.Ancestors {
		trimmed := strings.TrimSpace(ancestor)
		if trimmed == "" {
			return fmt.Errorf("dingtalk resource id has empty ancestor")
		}
		if trimmed != ancestor {
			return fmt.Errorf("dingtalk resource id has non-canonical ancestor")
		}
		if _, exists := seen[ancestor]; exists {
			return fmt.Errorf("dingtalk resource id contains an ancestor cycle")
		}
		seen[ancestor] = struct{}{}
	}
	if ref.NodeID != "" {
		if _, exists := seen[ref.NodeID]; exists {
			return fmt.Errorf("dingtalk resource id contains a node cycle")
		}
	}
	return nil
}

func ancestorResourceIDs(ref resourceRef) ([]string, error) {
	workspaceID, err := encodeResourceRef(workspaceResourceRef(ref.WorkspaceID))
	if err != nil {
		return nil, err
	}
	out := []string{workspaceID}
	for i, nodeID := range ref.Ancestors {
		ancestorID, err := encodeResourceRef(resourceRef{
			WorkspaceID: ref.WorkspaceID,
			NodeID:      nodeID,
			Ancestors:   append([]string(nil), ref.Ancestors[:i]...),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, ancestorID)
	}
	return out, nil
}
