package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// ExecutionScopeHashVersion identifies the canonical execution-scope payload.
const ExecutionScopeHashVersion = "knowledge_scope_execution/v1"

type knowledgeScopeHashPayload struct {
	Version string                     `json:"version"`
	Targets []knowledgeScopeHashTarget `json:"targets"`
}

type knowledgeScopeHashTarget struct {
	KnowledgeBaseID string                         `json:"knowledge_base_id"`
	SourceTenantID  uint64                         `json:"source_tenant_id"`
	KnowledgeIDs    []string                       `json:"knowledge_ids"`
	TagIDs          []string                       `json:"tag_ids"`
	ScopeTagIDs     []string                       `json:"scope_tag_ids"`
	FolderFilter    knowledgeScopeHashFolderFilter `json:"folder_filter"`
}

type knowledgeScopeHashFolderFilter struct {
	Enabled   bool     `json:"enabled"`
	FolderIDs []string `json:"folder_ids"`
}

// HashKnowledgeScope returns the stable SHA-256 hash of an execution scope.
// The hash identifies scope semantics; it is not an authorization credential.
func HashKnowledgeScope(scope *KnowledgeScope) (string, error) {
	if scope == nil {
		return "", fmt.Errorf(
			"%w: execution scope is nil",
			ErrInvalidKnowledgeScopeRequest,
		)
	}

	targets := scope.Targets()
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].sourceTenantID != targets[j].sourceTenantID {
			return targets[i].sourceTenantID < targets[j].sourceTenantID
		}
		return targets[i].knowledgeBaseID < targets[j].knowledgeBaseID
	})

	payloadTargets := make([]knowledgeScopeHashTarget, 0, len(targets))
	for _, target := range targets {
		folderIDs := []string{}
		if target.folderFilter.enabled {
			folderIDs = canonicalKnowledgeScopeHashIDs(
				target.folderFilter.folderIDs,
			)
		}
		payloadTargets = append(payloadTargets, knowledgeScopeHashTarget{
			KnowledgeBaseID: target.knowledgeBaseID,
			SourceTenantID:  target.sourceTenantID,
			KnowledgeIDs: canonicalKnowledgeScopeHashIDs(
				target.knowledgeIDs,
			),
			TagIDs: canonicalKnowledgeScopeHashIDs(
				target.tagIDs,
			),
			ScopeTagIDs: canonicalKnowledgeScopeHashIDs(
				target.scopeTagIDs,
			),
			FolderFilter: knowledgeScopeHashFolderFilter{
				Enabled:   target.folderFilter.enabled,
				FolderIDs: folderIDs,
			},
		})
	}

	encoded, err := json.Marshal(knowledgeScopeHashPayload{
		Version: ExecutionScopeHashVersion,
		Targets: payloadTargets,
	})
	if err != nil {
		return "", fmt.Errorf("marshal execution scope hash payload: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func canonicalKnowledgeScopeHashIDs(values []string) []string {
	sorted := make([]string, len(values))
	copy(sorted, values)
	sort.Strings(sorted)
	if len(sorted) < 2 {
		return sorted
	}

	deduplicated := sorted[:1]
	for _, value := range sorted[1:] {
		if value != deduplicated[len(deduplicated)-1] {
			deduplicated = append(deduplicated, value)
		}
	}
	return deduplicated
}
