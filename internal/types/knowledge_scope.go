package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// ErrInvalidKnowledgeScopeRequest identifies malformed logical scope input.
var ErrInvalidKnowledgeScopeRequest = errors.New("invalid knowledge scope request")

// ErrKnowledgeScopeTooLarge identifies a per-request execution budget breach.
var ErrKnowledgeScopeTooLarge = errors.New("knowledge scope is too large")

// FolderScopeRequest selects folders within one knowledge base.
type FolderScopeRequest struct {
	KnowledgeBaseID    string   `json:"knowledge_base_id"`
	FolderIDs          []string `json:"folder_ids"`
	IncludeDescendants *bool    `json:"include_descendants,omitempty"`
}

// KnowledgeScopeRequest is a serializable logical request without authorization
// results. Historical requests must be authorized again before resolution.
type KnowledgeScopeRequest struct {
	KnowledgeBaseIDs []string              `json:"knowledge_base_ids,omitempty"`
	KnowledgeIDs     []string              `json:"knowledge_ids,omitempty"`
	TagScopes        []TagScope            `json:"tag_scopes,omitempty"`
	FolderScopes     *[]FolderScopeRequest `json:"folder_scopes,omitempty"`
}

type knowledgeScopeRequestJSONAlias KnowledgeScopeRequest

// MarshalJSON preserves the enabled-empty folder-scope state.
func (r KnowledgeScopeRequest) MarshalJSON() ([]byte, error) {
	copyValue := knowledgeScopeRequestJSONAlias(r)
	if r.FolderScopes != nil && *r.FolderScopes == nil {
		empty := make([]FolderScopeRequest, 0)
		copyValue.FolderScopes = &empty
	}
	return json.Marshal(copyValue)
}

// AuthorizedKnowledgeScopeTarget is an already-authorized resolver input.
type AuthorizedKnowledgeScopeTarget struct {
	KnowledgeBaseID string   `json:"-"`
	SourceTenantID  uint64   `json:"-"`
	KnowledgeIDs    []string `json:"-"`
	TagIDs          []string `json:"-"`
	ScopeTagIDs     []string `json:"-"`
}

// String returns a redacted authorized-target summary.
func (t AuthorizedKnowledgeScopeTarget) String() string {
	return fmt.Sprintf(
		"AuthorizedKnowledgeScopeTarget{knowledge_ids=%d, tag_ids=%d, scope_tag_ids=%d}",
		len(t.KnowledgeIDs),
		len(t.TagIDs),
		len(t.ScopeTagIDs),
	)
}

// GoString returns a redacted authorized-target summary.
func (t AuthorizedKnowledgeScopeTarget) GoString() string {
	return t.String()
}

// KnowledgeScopeResolveInput combines request semantics with authorized targets.
type KnowledgeScopeResolveInput struct {
	Request           *KnowledgeScopeRequest           `json:"-"`
	AuthorizedTargets []AuthorizedKnowledgeScopeTarget `json:"-"`
}

// String returns a redacted resolver-input summary.
func (i KnowledgeScopeResolveInput) String() string {
	return fmt.Sprintf(
		"KnowledgeScopeResolveInput{request_present=%t, authorized_targets=%d}",
		i.Request != nil,
		len(i.AuthorizedTargets),
	)
}

// GoString returns a redacted resolver-input summary.
func (i KnowledgeScopeResolveInput) GoString() string {
	return i.String()
}

// ResolvedFolderFilter is immutable after construction. A disabled filter
// means all folders, while an enabled filter may intentionally be empty.
type ResolvedFolderFilter struct {
	enabled   bool
	folderIDs []string
}

// KnowledgeScopeTarget is one immutable, authorized execution target.
type KnowledgeScopeTarget struct {
	knowledgeBaseID string
	sourceTenantID  uint64
	knowledgeIDs    []string
	tagIDs          []string
	scopeTagIDs     []string
	folderFilter    ResolvedFolderFilter
}

// KnowledgeScope is an immutable execution scope whose getters return copies.
type KnowledgeScope struct {
	targets []KnowledgeScopeTarget
}

// KnowledgeScopePreparation keeps the serializable request separate from its
// authorized, resolved execution scope.
type KnowledgeScopePreparation struct {
	request            *KnowledgeScopeRequest
	execution          *KnowledgeScope
	executionScopeHash string
}

// KnowledgeScopePrepareInput carries logical client scope plus server-resolved
// QA defaults. Authentication and principal data remain context-only.
type KnowledgeScopePrepareInput struct {
	CanonicalRequest *KnowledgeScopeRequest
	LegacyRequest    *KnowledgeScopeRequest
	Session          *Session
	CustomAgent      *CustomAgent
	// SharedAgent is set only after server-side share authorization.
	SharedAgent bool
}

type normalizedFolderScopeGroup struct {
	seenEntry     bool
	rootRecursive bool
	selectors     map[string]bool
}

// Clone returns an ownership-independent request copy.
func (r *KnowledgeScopeRequest) Clone() *KnowledgeScopeRequest {
	if r == nil {
		return nil
	}

	cloned := &KnowledgeScopeRequest{
		KnowledgeBaseIDs: cloneKnowledgeScopeRequestStrings(r.KnowledgeBaseIDs),
		KnowledgeIDs:     cloneKnowledgeScopeRequestStrings(r.KnowledgeIDs),
	}
	if r.TagScopes != nil {
		cloned.TagScopes = make([]TagScope, len(r.TagScopes))
		for index, scope := range r.TagScopes {
			cloned.TagScopes[index] = TagScope{
				KnowledgeBaseID: scope.KnowledgeBaseID,
				TagIDs:          cloneKnowledgeScopeRequestStrings(scope.TagIDs),
			}
		}
	}
	if r.FolderScopes != nil {
		folderScopes := make([]FolderScopeRequest, len(*r.FolderScopes))
		for index, scope := range *r.FolderScopes {
			folderScopes[index] = FolderScopeRequest{
				KnowledgeBaseID: scope.KnowledgeBaseID,
				FolderIDs:       cloneKnowledgeScopeRequestStrings(scope.FolderIDs),
			}
			if scope.IncludeDescendants != nil {
				includeDescendants := *scope.IncludeDescendants
				folderScopes[index].IncludeDescendants = &includeDescendants
			}
		}
		cloned.FolderScopes = &folderScopes
	}
	return cloned
}

// NormalizeKnowledgeScopeRequest returns a canonical copy of a request.
func NormalizeKnowledgeScopeRequest(in *KnowledgeScopeRequest) (*KnowledgeScopeRequest, error) {
	if in == nil {
		return nil, nil
	}

	knowledgeBaseIDs, err := normalizeKnowledgeScopeIDs(in.KnowledgeBaseIDs)
	if err != nil {
		return nil, err
	}
	knowledgeIDs, err := normalizeKnowledgeScopeIDs(in.KnowledgeIDs)
	if err != nil {
		return nil, err
	}
	tagScopes, err := normalizeKnowledgeScopeTagScopes(in.TagScopes)
	if err != nil {
		return nil, err
	}
	folderScopes, err := normalizeKnowledgeScopeFolderScopes(in.FolderScopes)
	if err != nil {
		return nil, err
	}

	return &KnowledgeScopeRequest{
		KnowledgeBaseIDs: knowledgeBaseIDs,
		KnowledgeIDs:     knowledgeIDs,
		TagScopes:        tagScopes,
		FolderScopes:     folderScopes,
	}, nil
}

// ReconcileKnowledgeScopeRequest chooses canonical input without unioning legacy scope.
func ReconcileKnowledgeScopeRequest(
	canonical *KnowledgeScopeRequest,
	legacy *KnowledgeScopeRequest,
) (*KnowledgeScopeRequest, error) {
	if canonical == nil {
		return NormalizeKnowledgeScopeRequest(legacy)
	}

	normalizedCanonical, err := NormalizeKnowledgeScopeRequest(canonical)
	if err != nil {
		return nil, err
	}
	if legacy == nil {
		return normalizedCanonical, nil
	}

	normalizedLegacy, err := NormalizeKnowledgeScopeRequest(legacy)
	if err != nil {
		return nil, err
	}
	if !equivalentLegacyExpressibleProjection(normalizedCanonical, normalizedLegacy) {
		return nil, fmt.Errorf(
			"%w: canonical and legacy scope differ",
			ErrInvalidKnowledgeScopeRequest,
		)
	}
	return normalizedCanonical, nil
}

// EquivalentKnowledgeScopeRequest compares complete normalized request semantics.
func EquivalentKnowledgeScopeRequest(
	left *KnowledgeScopeRequest,
	right *KnowledgeScopeRequest,
) (bool, error) {
	normalizedLeft, err := NormalizeKnowledgeScopeRequest(left)
	if err != nil {
		return false, err
	}
	normalizedRight, err := NormalizeKnowledgeScopeRequest(right)
	if err != nil {
		return false, err
	}
	return equalNormalizedKnowledgeScopeRequests(normalizedLeft, normalizedRight), nil
}

// NewResolvedFolderFilter constructs an immutable resolved filter.
func NewResolvedFolderFilter(
	enabled bool,
	folderIDs []string,
) (ResolvedFolderFilter, error) {
	if !enabled {
		if len(folderIDs) != 0 {
			return ResolvedFolderFilter{}, fmt.Errorf(
				"%w: disabled folder filter cannot contain folder IDs",
				ErrInvalidKnowledgeScopeRequest,
			)
		}
		return ResolvedFolderFilter{}, nil
	}
	normalized, err := normalizeKnowledgeScopeFolderIDs(folderIDs)
	if err != nil {
		return ResolvedFolderFilter{}, err
	}
	return ResolvedFolderFilter{
		enabled:   true,
		folderIDs: normalized,
	}, nil
}

// Enabled reports whether folder filtering is active.
func (f ResolvedFolderFilter) Enabled() bool {
	return f.enabled
}

// FolderIDs returns a copy of the resolved folder IDs.
func (f ResolvedFolderFilter) FolderIDs() []string {
	return cloneKnowledgeScopeStrings(f.folderIDs)
}

// Empty reports whether this is an enabled-empty filter.
func (f ResolvedFolderFilter) Empty() bool {
	return f.enabled && len(f.folderIDs) == 0
}

// String returns a redacted filter summary.
func (f ResolvedFolderFilter) String() string {
	return fmt.Sprintf(
		"ResolvedFolderFilter{enabled=%t, folder_ids=%d, empty=%t}",
		f.enabled,
		len(f.folderIDs),
		f.Empty(),
	)
}

// GoString returns a redacted filter summary.
func (f ResolvedFolderFilter) GoString() string {
	return f.String()
}

// Clone returns an independent filter copy.
func (f ResolvedFolderFilter) Clone() ResolvedFolderFilter {
	return ResolvedFolderFilter{
		enabled:   f.enabled,
		folderIDs: cloneKnowledgeScopeStrings(f.folderIDs),
	}
}

// NewKnowledgeScopeTarget constructs an immutable execution target.
func NewKnowledgeScopeTarget(
	knowledgeBaseID string,
	sourceTenantID uint64,
	knowledgeIDs []string,
	tagIDs []string,
	scopeTagIDs []string,
	folderFilter ResolvedFolderFilter,
) (KnowledgeScopeTarget, error) {
	knowledgeBaseID, err := normalizeRequiredKnowledgeScopeID(
		knowledgeBaseID,
		"knowledge base id",
	)
	if err != nil {
		return KnowledgeScopeTarget{}, err
	}
	if sourceTenantID == 0 {
		return KnowledgeScopeTarget{}, fmt.Errorf(
			"%w: source tenant id is empty",
			ErrInvalidKnowledgeScopeRequest,
		)
	}
	knowledgeIDs, err = normalizeKnowledgeScopeIDs(knowledgeIDs)
	if err != nil {
		return KnowledgeScopeTarget{}, err
	}
	tagIDs, err = normalizeKnowledgeScopeIDs(tagIDs)
	if err != nil {
		return KnowledgeScopeTarget{}, err
	}
	scopeTagIDs, err = normalizeKnowledgeScopeIDs(scopeTagIDs)
	if err != nil {
		return KnowledgeScopeTarget{}, err
	}

	return KnowledgeScopeTarget{
		knowledgeBaseID: knowledgeBaseID,
		sourceTenantID:  sourceTenantID,
		knowledgeIDs:    knowledgeIDs,
		tagIDs:          tagIDs,
		scopeTagIDs:     scopeTagIDs,
		folderFilter:    folderFilter.Clone(),
	}, nil
}

// KnowledgeBaseID returns the target knowledge base ID.
func (t KnowledgeScopeTarget) KnowledgeBaseID() string {
	return t.knowledgeBaseID
}

// SourceTenantID returns the authoritative source tenant.
func (t KnowledgeScopeTarget) SourceTenantID() uint64 {
	return t.sourceTenantID
}

// KnowledgeIDs returns an independent ID slice.
func (t KnowledgeScopeTarget) KnowledgeIDs() []string {
	return cloneKnowledgeScopeStrings(t.knowledgeIDs)
}

// TagIDs returns an independent physical tag slice.
func (t KnowledgeScopeTarget) TagIDs() []string {
	return cloneKnowledgeScopeStrings(t.tagIDs)
}

// ScopeTagIDs returns an independent logical tag slice.
func (t KnowledgeScopeTarget) ScopeTagIDs() []string {
	return cloneKnowledgeScopeStrings(t.scopeTagIDs)
}

// FolderFilter returns an independent filter copy.
func (t KnowledgeScopeTarget) FolderFilter() ResolvedFolderFilter {
	return t.folderFilter.Clone()
}

// String returns a redacted target summary.
func (t KnowledgeScopeTarget) String() string {
	return fmt.Sprintf(
		"KnowledgeScopeTarget{knowledge_ids=%d, tag_ids=%d, scope_tag_ids=%d, folder_enabled=%t, folder_ids=%d}",
		len(t.knowledgeIDs),
		len(t.tagIDs),
		len(t.scopeTagIDs),
		t.folderFilter.enabled,
		len(t.folderFilter.folderIDs),
	)
}

// GoString returns a redacted target summary.
func (t KnowledgeScopeTarget) GoString() string {
	return t.String()
}

// Clone returns an independent target copy.
func (t KnowledgeScopeTarget) Clone() KnowledgeScopeTarget {
	return KnowledgeScopeTarget{
		knowledgeBaseID: t.knowledgeBaseID,
		sourceTenantID:  t.sourceTenantID,
		knowledgeIDs:    cloneKnowledgeScopeStrings(t.knowledgeIDs),
		tagIDs:          cloneKnowledgeScopeStrings(t.tagIDs),
		scopeTagIDs:     cloneKnowledgeScopeStrings(t.scopeTagIDs),
		folderFilter:    t.folderFilter.Clone(),
	}
}

// NewKnowledgeScope constructs an immutable, deterministically ordered scope.
func NewKnowledgeScope(targets []KnowledgeScopeTarget) (*KnowledgeScope, error) {
	normalized := make([]KnowledgeScopeTarget, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, input := range targets {
		target, err := NewKnowledgeScopeTarget(
			input.knowledgeBaseID,
			input.sourceTenantID,
			input.knowledgeIDs,
			input.tagIDs,
			input.scopeTagIDs,
			input.folderFilter,
		)
		if err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%d\x00%s", target.sourceTenantID, target.knowledgeBaseID)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf(
				"%w: duplicate authorized target",
				ErrInvalidKnowledgeScopeRequest,
			)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, target)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].sourceTenantID != normalized[j].sourceTenantID {
			return normalized[i].sourceTenantID < normalized[j].sourceTenantID
		}
		return normalized[i].knowledgeBaseID < normalized[j].knowledgeBaseID
	})
	return &KnowledgeScope{targets: normalized}, nil
}

// Targets returns a deep copy of every target.
func (s *KnowledgeScope) Targets() []KnowledgeScopeTarget {
	if s == nil {
		return []KnowledgeScopeTarget{}
	}
	targets := make([]KnowledgeScopeTarget, len(s.targets))
	for index := range s.targets {
		targets[index] = s.targets[index].Clone()
	}
	return targets
}

// Len returns the target count.
func (s *KnowledgeScope) Len() int {
	if s == nil {
		return 0
	}
	return len(s.targets)
}

// IsEmpty reports whether the scope has no targets.
func (s *KnowledgeScope) IsEmpty() bool {
	return s == nil || len(s.targets) == 0
}

// HasLocalKnowledge reports whether any target may contain local knowledge.
func (s *KnowledgeScope) HasLocalKnowledge() bool {
	if s == nil {
		return false
	}
	for _, target := range s.targets {
		if !target.folderFilter.Empty() {
			return true
		}
	}
	return false
}

// HasEnabledNonEmptyFolderFilter reports whether Phase 5 filtering is needed.
func (s *KnowledgeScope) HasEnabledNonEmptyFolderFilter() bool {
	if s == nil {
		return false
	}
	for _, target := range s.targets {
		if target.folderFilter.enabled &&
			len(target.folderFilter.folderIDs) > 0 {
			return true
		}
	}
	return false
}

// String returns a redacted execution-scope summary.
func (s KnowledgeScope) String() string {
	return fmt.Sprintf(
		"KnowledgeScope{targets=%d, local=%t}",
		len(s.targets),
		s.HasLocalKnowledge(),
	)
}

// GoString returns a redacted execution-scope summary.
func (s KnowledgeScope) GoString() string {
	return s.String()
}

// Clone returns a deep copy of the scope.
func (s *KnowledgeScope) Clone() *KnowledgeScope {
	if s == nil {
		return nil
	}
	return &KnowledgeScope{targets: s.Targets()}
}

// NewKnowledgeScopePreparation constructs an ownership-independent result.
func NewKnowledgeScopePreparation(
	request *KnowledgeScopeRequest,
	execution *KnowledgeScope,
	executionScopeHash string,
) (*KnowledgeScopePreparation, error) {
	if request == nil || execution == nil || strings.TrimSpace(executionScopeHash) == "" {
		return nil, fmt.Errorf(
			"%w: incomplete knowledge scope preparation",
			ErrInvalidKnowledgeScopeRequest,
		)
	}
	return &KnowledgeScopePreparation{
		request:            request.Clone(),
		execution:          execution.Clone(),
		executionScopeHash: executionScopeHash,
	}, nil
}

// Request returns an independent serializable request scope.
func (p *KnowledgeScopePreparation) Request() *KnowledgeScopeRequest {
	if p == nil {
		return nil
	}
	return p.request.Clone()
}

// Execution returns an independent authorized execution scope.
func (p *KnowledgeScopePreparation) Execution() *KnowledgeScope {
	if p == nil {
		return nil
	}
	return p.execution.Clone()
}

// ExecutionScopeHash returns the stable execution-scope identifier.
func (p *KnowledgeScopePreparation) ExecutionScopeHash() string {
	if p == nil {
		return ""
	}
	return p.executionScopeHash
}

// HasEnabledNonEmptyFolderFilter reports whether Phase 5 filtering is needed.
func (p *KnowledgeScopePreparation) HasEnabledNonEmptyFolderFilter() bool {
	if p == nil || p.execution == nil {
		return false
	}
	return p.execution.HasEnabledNonEmptyFolderFilter()
}

// String returns a redacted preparation summary.
func (p KnowledgeScopePreparation) String() string {
	targets := 0
	local := false
	if p.execution != nil {
		targets = p.execution.Len()
		local = p.execution.HasLocalKnowledge()
	}
	return fmt.Sprintf(
		"KnowledgeScopePreparation{request_present=%t, targets=%d, local=%t, hash_set=%t}",
		p.request != nil,
		targets,
		local,
		p.executionScopeHash != "",
	)
}

// GoString returns a redacted preparation summary.
func (p KnowledgeScopePreparation) GoString() string {
	return p.String()
}

func normalizeKnowledgeScopeTagScopes(scopes []TagScope) ([]TagScope, error) {
	byKnowledgeBase := make(map[string]map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		knowledgeBaseID, err := normalizeRequiredKnowledgeScopeID(
			scope.KnowledgeBaseID,
			"tag scope knowledge base id",
		)
		if err != nil {
			return nil, err
		}
		tagIDs, err := normalizeKnowledgeScopeIDs(scope.TagIDs)
		if err != nil {
			return nil, err
		}
		if len(tagIDs) == 0 {
			return nil, fmt.Errorf(
				"%w: tag scope tag ids are empty",
				ErrInvalidKnowledgeScopeRequest,
			)
		}
		if byKnowledgeBase[knowledgeBaseID] == nil {
			byKnowledgeBase[knowledgeBaseID] = make(map[string]struct{}, len(tagIDs))
		}
		for _, tagID := range tagIDs {
			byKnowledgeBase[knowledgeBaseID][tagID] = struct{}{}
		}
	}

	knowledgeBaseIDs := make([]string, 0, len(byKnowledgeBase))
	for knowledgeBaseID := range byKnowledgeBase {
		knowledgeBaseIDs = append(knowledgeBaseIDs, knowledgeBaseID)
	}
	sort.Strings(knowledgeBaseIDs)

	result := make([]TagScope, 0, len(knowledgeBaseIDs))
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		tagIDs := make([]string, 0, len(byKnowledgeBase[knowledgeBaseID]))
		for tagID := range byKnowledgeBase[knowledgeBaseID] {
			tagIDs = append(tagIDs, tagID)
		}
		sort.Strings(tagIDs)
		result = append(result, TagScope{
			KnowledgeBaseID: knowledgeBaseID,
			TagIDs:          tagIDs,
		})
	}
	return result, nil
}

func normalizeKnowledgeScopeFolderScopes(
	scopes *[]FolderScopeRequest,
) (*[]FolderScopeRequest, error) {
	if scopes == nil {
		return nil, nil
	}
	if len(*scopes) == 0 {
		empty := []FolderScopeRequest{}
		return &empty, nil
	}

	groups := make(map[string]*normalizedFolderScopeGroup)
	for _, scope := range *scopes {
		knowledgeBaseID, err := normalizeRequiredKnowledgeScopeID(
			scope.KnowledgeBaseID,
			"folder scope knowledge base id",
		)
		if err != nil {
			return nil, err
		}
		folderIDs, err := normalizeKnowledgeScopeFolderIDs(scope.FolderIDs)
		if err != nil {
			return nil, err
		}
		group := groups[knowledgeBaseID]
		if group == nil {
			group = &normalizedFolderScopeGroup{
				selectors: make(map[string]bool),
			}
			groups[knowledgeBaseID] = group
		}
		group.seenEntry = true
		recursive := scope.IncludeDescendants == nil || *scope.IncludeDescendants
		for _, folderID := range folderIDs {
			if folderID == KnowledgeFolderRootID && recursive {
				group.rootRecursive = true
				continue
			}
			if current, exists := group.selectors[folderID]; !exists || recursive || !current {
				group.selectors[folderID] = recursive
			}
		}
	}

	for _, group := range groups {
		if !group.rootRecursive {
			continue
		}
		for folderID := range group.selectors {
			if folderID != KnowledgeFolderRootID {
				return nil, fmt.Errorf(
					"%w: recursive root cannot be combined with a non-root folder",
					ErrInvalidKnowledgeScopeRequest,
				)
			}
		}
	}

	knowledgeBaseIDs := make([]string, 0, len(groups))
	for knowledgeBaseID := range groups {
		knowledgeBaseIDs = append(knowledgeBaseIDs, knowledgeBaseID)
	}
	sort.Strings(knowledgeBaseIDs)

	result := make([]FolderScopeRequest, 0, len(groups)*2)
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		group := groups[knowledgeBaseID]
		if group.rootRecursive {
			// A recursive virtual root is the whole knowledge base.
			result = append(result, FolderScopeRequest{
				KnowledgeBaseID:    knowledgeBaseID,
				FolderIDs:          []string{KnowledgeFolderRootID},
				IncludeDescendants: knowledgeScopeBoolPointer(true),
			})
			continue
		}

		direct := make([]string, 0, len(group.selectors))
		recursive := make([]string, 0, len(group.selectors))
		for folderID, includeDescendants := range group.selectors {
			if includeDescendants {
				recursive = append(recursive, folderID)
			} else {
				direct = append(direct, folderID)
			}
		}
		sort.Strings(direct)
		sort.Strings(recursive)
		if len(direct) > 0 {
			result = append(result, FolderScopeRequest{
				KnowledgeBaseID:    knowledgeBaseID,
				FolderIDs:          direct,
				IncludeDescendants: knowledgeScopeBoolPointer(false),
			})
		}
		if len(recursive) > 0 {
			result = append(result, FolderScopeRequest{
				KnowledgeBaseID:    knowledgeBaseID,
				FolderIDs:          recursive,
				IncludeDescendants: knowledgeScopeBoolPointer(true),
			})
		}
		if len(direct) == 0 && len(recursive) == 0 && group.seenEntry {
			result = append(result, FolderScopeRequest{
				KnowledgeBaseID:    knowledgeBaseID,
				FolderIDs:          []string{},
				IncludeDescendants: knowledgeScopeBoolPointer(true),
			})
		}
	}
	return &result, nil
}

func normalizeKnowledgeScopeIDs(values []string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return nil, fmt.Errorf(
				"%w: id is empty or contains surrounding whitespace",
				ErrInvalidKnowledgeScopeRequest,
			)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeRequiredKnowledgeScopeID(value string, fieldName string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return "", fmt.Errorf(
			"%w: %s is invalid",
			ErrInvalidKnowledgeScopeRequest,
			fieldName,
		)
	}
	return value, nil
}

func normalizeKnowledgeScopeFolderIDs(values []string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		folderID, err := normalizeKnowledgeScopeFolderID(value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[folderID]; exists {
			continue
		}
		seen[folderID] = struct{}{}
		normalized = append(normalized, folderID)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeKnowledgeScopeFolderID(value string) (string, error) {
	if value == KnowledgeFolderRootID {
		return KnowledgeFolderRootID, nil
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf(
			"%w: folder id contains surrounding whitespace",
			ErrInvalidKnowledgeScopeRequest,
		)
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", fmt.Errorf(
			"%w: folder id is not a canonical UUID",
			ErrInvalidKnowledgeScopeRequest,
		)
	}
	canonical := parsed.String()
	if canonical != value {
		return "", fmt.Errorf(
			"%w: folder id is not a canonical UUID",
			ErrInvalidKnowledgeScopeRequest,
		)
	}
	return value, nil
}

func equalNormalizedKnowledgeScopeRequests(
	left *KnowledgeScopeRequest,
	right *KnowledgeScopeRequest,
) bool {
	if left == nil || right == nil {
		return left == right
	}
	if !slices.Equal(left.KnowledgeBaseIDs, right.KnowledgeBaseIDs) ||
		!slices.Equal(left.KnowledgeIDs, right.KnowledgeIDs) ||
		!equalKnowledgeScopeTagScopes(left.TagScopes, right.TagScopes) {
		return false
	}
	return equalKnowledgeScopeFolderScopes(left.FolderScopes, right.FolderScopes)
}

func equivalentLegacyExpressibleProjection(
	canonical *KnowledgeScopeRequest,
	legacy *KnowledgeScopeRequest,
) bool {
	canonicalProjection := canonical.Clone()
	legacyProjection := legacy.Clone()
	canonicalProjection.KnowledgeBaseIDs = nil
	legacyProjection.KnowledgeBaseIDs = nil
	if legacyProjection.FolderScopes == nil {
		canonicalProjection.FolderScopes = nil
	}
	if !equalNormalizedKnowledgeScopeRequests(
		canonicalProjection,
		legacyProjection,
	) {
		return false
	}

	canonicalUniverse := knowledgeScopeRequestUniverse(canonical)
	legacyUniverse := knowledgeScopeRequestUniverse(legacy)
	if len(canonicalUniverse) > 0 && len(legacyUniverse) > 0 {
		return slices.Equal(canonicalUniverse, legacyUniverse)
	}
	return true
}

func knowledgeScopeRequestUniverse(request *KnowledgeScopeRequest) []string {
	if request == nil {
		return []string{}
	}
	seen := make(map[string]struct{})
	for _, knowledgeBaseID := range request.KnowledgeBaseIDs {
		seen[knowledgeBaseID] = struct{}{}
	}
	for _, tagScope := range request.TagScopes {
		seen[tagScope.KnowledgeBaseID] = struct{}{}
	}
	if request.FolderScopes != nil {
		for _, folderScope := range *request.FolderScopes {
			seen[folderScope.KnowledgeBaseID] = struct{}{}
		}
	}
	knowledgeBaseIDs := make([]string, 0, len(seen))
	for knowledgeBaseID := range seen {
		knowledgeBaseIDs = append(knowledgeBaseIDs, knowledgeBaseID)
	}
	sort.Strings(knowledgeBaseIDs)
	return knowledgeBaseIDs
}

func equalKnowledgeScopeTagScopes(left []TagScope, right []TagScope) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].KnowledgeBaseID != right[index].KnowledgeBaseID ||
			!slices.Equal(left[index].TagIDs, right[index].TagIDs) {
			return false
		}
	}
	return true
}

func equalKnowledgeScopeFolderScopes(
	left *[]FolderScopeRequest,
	right *[]FolderScopeRequest,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if len(*left) != len(*right) {
		return false
	}
	for index := range *left {
		leftScope := (*left)[index]
		rightScope := (*right)[index]
		if leftScope.KnowledgeBaseID != rightScope.KnowledgeBaseID ||
			!slices.Equal(leftScope.FolderIDs, rightScope.FolderIDs) ||
			effectiveKnowledgeScopeDescendants(leftScope.IncludeDescendants) !=
				effectiveKnowledgeScopeDescendants(rightScope.IncludeDescendants) {
			return false
		}
	}
	return true
}

func effectiveKnowledgeScopeDescendants(value *bool) bool {
	return value == nil || *value
}

func cloneKnowledgeScopeStrings(values []string) []string {
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneKnowledgeScopeRequestStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return cloneKnowledgeScopeStrings(values)
}

func knowledgeScopeBoolPointer(value bool) *bool {
	return &value
}
