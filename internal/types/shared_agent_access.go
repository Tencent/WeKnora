package types

// SharedAgentKBScope describes the KBs exposed by an already-authorized shared
// agent. It is not proof that a caller can use that agent. The zero value, an
// unknown selection mode, and an empty selection all deny access.
type SharedAgentKBScope struct {
	tenantID uint64
	all      bool
	ids      []string
	selected map[string]struct{}
}

// NewSharedAgentKBScope snapshots an authorized agent configuration.
func NewSharedAgentKBScope(agent *CustomAgent) SharedAgentKBScope {
	if agent == nil || agent.TenantID == 0 {
		return SharedAgentKBScope{}
	}
	scope := SharedAgentKBScope{tenantID: agent.TenantID}
	switch agent.Config.KBSelectionMode {
	case "all":
		scope.all = true
	case "selected":
		scope.selected = make(map[string]struct{}, len(agent.Config.KnowledgeBases))
		for _, id := range agent.Config.KnowledgeBases {
			if id == "" {
				continue
			}
			if _, exists := scope.selected[id]; exists {
				continue
			}
			scope.selected[id] = struct{}{}
			scope.ids = append(scope.ids, id)
		}
	}
	return scope
}

// IsAll reports whether every KB in the source tenant is selected.
func (s SharedAgentKBScope) IsAll() bool { return s.all }

// IsEmpty reports whether the scope authorizes no KBs.
func (s SharedAgentKBScope) IsEmpty() bool { return !s.all && len(s.ids) == 0 }

// IDs returns a copy of the explicit selection. Use IsAll to distinguish a
// whole-tenant scope from an empty selection; nil never means unrestricted.
func (s SharedAgentKBScope) IDs() []string { return append([]string{}, s.ids...) }

// Allows checks both the KB selection and the authoritative owner tenant.
func (s SharedAgentKBScope) Allows(kbID string, tenantID uint64) bool {
	if kbID == "" || s.tenantID == 0 || s.tenantID != tenantID {
		return false
	}
	_, selected := s.selected[kbID]
	return s.all || selected
}

// SharedAgentIncludesKB also binds an "all" selection to the agent's tenant.
func SharedAgentIncludesKB(agent *CustomAgent, kb *KnowledgeBase) bool {
	return kb != nil && NewSharedAgentKBScope(agent).Allows(kb.ID, kb.TenantID)
}
