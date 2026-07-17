package cmdutil

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	sdk "github.com/Tencent/WeKnora/client"
)

// uuidPattern matches the canonical 8-4-4-4-12 UUID form. WeKnora's KB ids
// are uuid.New().String() output stored as varchar(36); names are arbitrary
// user-supplied strings, so format-detection is unambiguous.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsKBID reports whether s looks like a KB id. Used by Factory.ResolveKB and
// any caller that accepts a single id-or-name selector value (id vs name
// auto-detection).
func IsKBID(s string) bool { return uuidPattern.MatchString(s) }

// KBLister is the narrow SDK surface ResolveKBNameToID depends on. The
// production *sdk.Client satisfies it; tests inject fakes without standing
// up an HTTP server.
type KBLister interface {
	ListKnowledgeBases(ctx context.Context) ([]sdk.KnowledgeBase, error)
}

// SharedKBLister is the SDK surface for knowledge bases made visible through
// a shared space.
type SharedKBLister interface {
	ListSharedKnowledgeBases(ctx context.Context) ([]sdk.SharedKnowledgeBaseInfo, error)
}

// VisibleKBLister can enumerate both current-workspace and cross-tenant shared
// knowledge bases. The production SDK client implements this interface.
type VisibleKBLister interface {
	KBLister
	SharedKBLister
}

// VisibleKnowledgeBase is the flattened representation used by CLI discovery
// commands. Owned rows retain the KnowledgeBase wire shape; shared rows add
// is_shared=true plus the space and effective-permission metadata needed to
// explain why the active profile can see them.
type VisibleKnowledgeBase struct {
	sdk.KnowledgeBase
	IsShared       bool       `json:"is_shared,omitempty"`
	ShareID        string     `json:"share_id,omitempty"`
	OrganizationID string     `json:"organization_id,omitempty"`
	OrgName        string     `json:"org_name,omitempty"`
	Permission     string     `json:"permission,omitempty"`
	SourceTenantID uint64     `json:"source_tenant_id,omitempty"`
	SharedAt       *time.Time `json:"shared_at,omitempty"`
}

// UnmarshalJSON handles KnowledgeBase's custom unmarshaller explicitly. Without
// this method, the anonymous embedded type's UnmarshalJSON is promoted and the
// sharing metadata is silently discarded when MCP/test clients decode output.
func (kb *VisibleKnowledgeBase) UnmarshalJSON(data []byte) error {
	var base sdk.KnowledgeBase
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	var sharing struct {
		IsShared       bool       `json:"is_shared"`
		ShareID        string     `json:"share_id"`
		OrganizationID string     `json:"organization_id"`
		OrgName        string     `json:"org_name"`
		Permission     string     `json:"permission"`
		SourceTenantID uint64     `json:"source_tenant_id"`
		SharedAt       *time.Time `json:"shared_at"`
	}
	if err := json.Unmarshal(data, &sharing); err != nil {
		return err
	}
	kb.KnowledgeBase = base
	kb.IsShared = sharing.IsShared
	kb.ShareID = sharing.ShareID
	kb.OrganizationID = sharing.OrganizationID
	kb.OrgName = sharing.OrgName
	kb.Permission = sharing.Permission
	kb.SourceTenantID = sharing.SourceTenantID
	kb.SharedAt = sharing.SharedAt
	return nil
}

// ListVisibleKnowledgeBases combines current-workspace and shared-space KBs.
// Callers choose which sources to include so `kb list --owned` and `--shared`
// avoid unnecessary requests. Owned rows win defensively if an API ever
// returns the same KB from both endpoints.
func ListVisibleKnowledgeBases(
	ctx context.Context, lister VisibleKBLister, includeOwned, includeShared bool,
) ([]VisibleKnowledgeBase, error) {
	var owned []sdk.KnowledgeBase
	var shared []sdk.SharedKnowledgeBaseInfo

	if includeOwned {
		var err error
		owned, err = lister.ListKnowledgeBases(ctx)
		if err != nil {
			return nil, err
		}
	}
	if includeShared {
		var err error
		shared, err = lister.ListSharedKnowledgeBases(ctx)
		if err != nil {
			// Preserve compatibility with servers predating the shared-list
			// endpoint when this is an aggregate discovery call. An explicit
			// --shared request still surfaces the 404.
			if includeOwned && ClassifyHTTPError(err) == CodeResourceNotFound {
				shared = nil
			} else {
				return nil, err
			}
		}
	}

	items := make([]VisibleKnowledgeBase, 0, len(owned)+len(shared))
	seen := make(map[string]struct{}, len(owned)+len(shared))
	for _, kb := range owned {
		items = append(items, VisibleKnowledgeBase{KnowledgeBase: kb})
		if kb.ID != "" {
			seen[kb.ID] = struct{}{}
		}
	}
	for _, info := range shared {
		if info.KnowledgeBase == nil {
			continue
		}
		if _, ok := seen[info.KnowledgeBase.ID]; info.KnowledgeBase.ID != "" && ok {
			continue
		}
		sharedAt := info.SharedAt
		items = append(items, VisibleKnowledgeBase{
			KnowledgeBase:  *info.KnowledgeBase,
			IsShared:       true,
			ShareID:        info.ShareID,
			OrganizationID: info.OrganizationID,
			OrgName:        info.OrgName,
			Permission:     info.Permission,
			SourceTenantID: info.SourceTenantID,
			SharedAt:       &sharedAt,
		})
		if info.KnowledgeBase.ID != "" {
			seen[info.KnowledgeBase.ID] = struct{}{}
		}
	}
	return items, nil
}

// ResolveKBFlag interprets a raw --kb value (id or name) and returns the
// canonical id. Pass-through when raw already looks like an id; otherwise
// list and match by name. Shared by every command that takes a --kb flag
// directly (search chunks/docs, doc download, link …) so the id-or-name
// policy never drifts.
func ResolveKBFlag(ctx context.Context, lister VisibleKBLister, raw string) (string, error) {
	if IsKBID(raw) {
		return raw, nil
	}
	return ResolveKBNameToID(ctx, lister, raw)
}

// ResolveKBNameToID looks up a knowledge base by name and returns its ID.
// Used by `link` and `Factory.ResolveKB` - a single lookup so the match
// policy (currently exact case-sensitive) lives in one place.
func ResolveKBNameToID(ctx context.Context, lister VisibleKBLister, name string) (string, error) {
	kbs, err := ListVisibleKnowledgeBases(ctx, lister, true, true)
	if err != nil {
		return "", WrapHTTP(err, "list knowledge bases")
	}
	matches := make([]VisibleKnowledgeBase, 0, 1)
	for _, kb := range kbs {
		if kb.Name == name {
			matches = append(matches, kb)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return "", NewError(CodeKBNotFound, fmt.Sprintf("knowledge base not found: %s", name))
	default:
		return "", NewError(CodeInputInvalidArgument,
			fmt.Sprintf("%q matches %d visible knowledge bases; pass the UUID instead", name, len(matches))).
			WithHint("run `weknora kb list` to inspect ids and sharing metadata")
	}
}
