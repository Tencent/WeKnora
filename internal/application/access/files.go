package access

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// FileAccess is a request-local result, produced before storage is opened.
// Physical locators are not a substitute for KB/message authorization.
type FileAccess struct {
	OwnerTenantID    uint64
	Path             string
	Filename         string
	StorageBackendID string
}

// FileCatalog resolves logical references to their stored owner and locator.
type FileCatalog interface {
	ResolvePath(context.Context, string) (string, *types.StoredResource, error)
}

func resolveFile(
	ctx context.Context,
	catalog FileCatalog,
	reference string,
) (FileAccess, *types.StoredResource, error) {
	file := FileAccess{Path: reference, Filename: reference}
	if catalog == nil {
		if _, resource := types.ParseResourcePath(reference); resource {
			return file, nil, ErrNotFound
		}
		return file, nil, nil
	}
	path, resource, err := catalog.ResolvePath(ctx, reference)
	if err != nil {
		return file, nil, ErrNotFound
	}
	file.Path = path
	file.Filename = path
	if resource != nil {
		file.OwnerTenantID = resource.TenantID
		file.StorageBackendID = resource.StorageBackendID
		if strings.TrimSpace(resource.OriginalName) != "" {
			file.Filename = resource.OriginalName
		}
	}
	return file, resource, nil
}

// ResolveKBFile requires the route's exact KB grant and an independent live
// explicit binding check. Rewriting the execution tenant is insufficient.
func ResolveKBFile(
	ctx context.Context,
	grant *KBAccess,
	kbID, reference string,
	catalog FileCatalog,
	bindings interfaces.KBResourceLookup,
) (FileAccess, error) {
	var empty FileAccess
	if grant == nil || grant.KnowledgeBase == nil || grant.KnowledgeBase.ID != kbID ||
		grant.Caller != types.CallerFromContext(ctx) || !grant.Permission.HasPermission(types.OrgRoleViewer) {
		return empty, ErrForbidden
	}
	if err := types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, kbID); err != nil {
		return empty, err
	}
	file, resource, err := resolveFile(ctx, catalog, reference)
	if err != nil {
		return empty, err
	}
	owner := grant.EffectiveTenantID
	if owner == 0 || (resource != nil && resource.TenantID != owner) {
		return empty, ErrForbidden
	}
	if err := secutils.ValidateKBScopedStoragePath(file.Path, owner); err != nil {
		return empty, ErrForbidden
	}
	if bindings == nil {
		return empty, ErrForbidden
	}
	bound, err := bindings.IsReferencedByKnowledgeBase(ctx, owner, kbID, reference)
	if err != nil {
		return empty, fmt.Errorf("check KB file binding: %w", err)
	}
	if !bound {
		return empty, ErrForbidden
	}
	file.OwnerTenantID = owner
	return file, nil
}

// MessageReferencesFile examines only persisted rendering/output fields. Tool
// arguments and request metadata are not evidence of a returned file.
func MessageReferencesFile(message *types.Message, reference string) bool {
	if message == nil {
		return false
	}
	if types.ContainsStorageReference(message.Content, reference) {
		return true
	}
	for _, artifact := range message.Artifacts {
		if artifact.URL == reference {
			return true
		}
	}
	for _, value := range []interface{}{message.KnowledgeReferences, message.Images} {
		data, _ := json.Marshal(value)
		if types.ContainsStorageReference(string(data), reference) {
			return true
		}
	}
	for _, step := range message.AgentSteps {
		for _, call := range step.ToolCalls {
			if call.Result == nil {
				continue
			}
			data, _ := json.Marshal(call.Result)
			if types.ContainsStorageReference(string(data), reference) {
				return true
			}
		}
	}
	return false
}

// ResolveMessageFile loads the session-authorized message using the original
// caller and rechecks its current sharing relationships on every request.
func ResolveMessageFile(ctx context.Context, sessionID, messageID, reference string, messages MessageFileLookup,
	agents SharedAgentFileLookup, catalog FileCatalog, kbShares MessageKBShareAuthorizer,
) (FileAccess, error) {
	caller := types.CallerFromContext(ctx)
	if caller.TenantID == 0 {
		return FileAccess{}, ErrUnauthorized
	}
	if messages == nil {
		return FileAccess{}, ErrNotFound
	}
	ctx = types.WithExecutionTenant(ctx, caller.TenantID)
	message, err := messages.GetMessage(ctx, sessionID, messageID)
	if err != nil || message == nil {
		return FileAccess{}, ErrNotFound
	}
	return AuthorizeMessageFile(ctx, message, reference, agents, catalog, kbShares)
}

// AuthorizeMessageFile also serves index-based artifact downloads after their
// session/message lookup. It never accepts an unchecked client message.
func AuthorizeMessageFile(ctx context.Context, message *types.Message, reference string, agents SharedAgentFileLookup,
	catalog FileCatalog, kbShares MessageKBShareAuthorizer,
) (FileAccess, error) {
	caller := types.CallerFromContext(ctx)
	if caller.TenantID == 0 {
		return FileAccess{}, ErrUnauthorized
	}
	if !MessageReferencesFile(message, reference) {
		return FileAccess{}, ErrForbidden
	}
	file, resource, err := resolveFile(ctx, catalog, reference)
	if err != nil {
		return FileAccess{}, err
	}
	owner := message.AgentTenantID
	if resource != nil {
		owner = resource.TenantID
	}
	if owner == 0 {
		return FileAccess{}, ErrForbidden
	}
	if message.Role == "user" && owner != caller.TenantID {
		return FileAccess{}, ErrForbidden
	}
	if kbShares.Bindings == nil {
		kbShares.Bindings, _ = catalog.(interfaces.KBResourceLookup)
	}
	kbAuthorized := false
	if resource != nil && message.AgentTenantID != 0 && message.AgentTenantID != owner {
		kbAuthorized = kbShares.resourceAccessibleViaSharedKB(ctx, message, resource, caller.TenantID, caller.Role)
		if !kbAuthorized {
			return FileAccess{}, ErrForbidden
		}
	}
	if owner != caller.TenantID && !kbAuthorized {
		if message.AgentTenantID == 0 {
			kbAuthorized = kbShares.resourceAccessibleViaSharedKB(ctx, message, resource, caller.TenantID, caller.Role)
		}
		if !kbAuthorized {
			if message.AgentID == "" || agents == nil {
				return FileAccess{}, ErrForbidden
			}
			agent, err := agents.GetSharedAgentForTenant(ctx, caller.TenantID, caller.Role, message.AgentID, owner)
			if err != nil || agent == nil || agent.TenantID != owner {
				return FileAccess{}, ErrForbidden
			}
			bindings, ok := catalog.(interfaces.MessageFileBindingLookup)
			if !ok {
				return FileAccess{}, ErrForbidden
			}
			origins, err := bindings.GetMessageFileBindings(ctx, owner, reference, message.ID)
			if err != nil || origins == nil {
				return FileAccess{}, ErrForbidden
			}
			allowed := origins.MessageArtifact
			scope := types.NewSharedAgentKBScope(agent)
			for _, kbID := range origins.KnowledgeBaseIDs {
				if scope.Allows(kbID, owner) && types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, kbID) == nil {
					allowed = true
				}
			}
			if !allowed {
				return FileAccess{}, ErrForbidden
			}
		}
	}
	if resource == nil {
		if err := secutils.ValidateStoragePathTenant(file.Path, owner); err != nil {
			return FileAccess{}, ErrForbidden
		}
	}
	file.OwnerTenantID = owner
	return file, nil
}

// ResolveMessageArtifact selects a persisted artifact after session ownership
// was checked. Session-owned output stays downloadable independently of the
// agent; source-owned output still requires its current sharing permission.
func ResolveMessageArtifact(ctx context.Context, message *types.Message, index int, agents SharedAgentFileLookup,
	catalog FileCatalog, kbShares MessageKBShareAuthorizer,
) (FileAccess, error) {
	caller := types.CallerFromContext(ctx)
	if caller.TenantID == 0 {
		return FileAccess{}, ErrUnauthorized
	}
	if message == nil || index < 0 || index >= len(message.Artifacts) {
		return FileAccess{}, ErrNotFound
	}
	artifact := message.Artifacts[index]
	file, resource, err := resolveFile(ctx, catalog, artifact.URL)
	if err != nil {
		return FileAccess{}, err
	}
	if (resource != nil && resource.TenantID == caller.TenantID) ||
		(resource == nil && secutils.ValidateStoragePathTenant(file.Path, caller.TenantID) == nil) {
		file.OwnerTenantID = caller.TenantID
		file.Filename = artifact.FileName
		return file, nil
	}
	file, err = AuthorizeMessageFile(ctx, message, artifact.URL, agents, catalog, kbShares)
	file.Filename = artifact.FileName
	return file, err
}
