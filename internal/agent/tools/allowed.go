package tools

// EffectiveAllowedTools returns the built-in tools an agent's allowed_tools
// value asks for. A nil list means the value was never set, as on agents
// saved before allowed_tools existed, and selects the default tools. Any
// other list, including an empty one, is taken as given: an empty list means
// no built-in tools. Retired names are rewritten to their successors.
//
// Tool registration, the session config and the rerank requirement all go
// through this function, so they cannot disagree about what an empty list
// means.
func EffectiveAllowedTools(allowed []string) []string {
	if allowed == nil {
		return DefaultAllowedTools()
	}
	if len(allowed) == 0 {
		return []string{}
	}
	return NormalizeAllowedTools(allowed)
}

// AllowedToolsCheck is the outcome of checking an allowed_tools list on save.
type AllowedToolsCheck struct {
	// Tools is the list to store: retired names rewritten, duplicates and
	// switch-managed tools removed. Nil when the request left the list unset.
	Tools []string
	// Rejected are names no built-in tool answers to that the request adds.
	Rejected []string
	// KeptUnknown are such names that were already stored. They are kept so
	// that an agent saved before an upgrade can still be saved.
	KeptUnknown []string
	// Dropped are names removed from the list because naming them there has
	// no effect: switch-managed tools, which other agent settings decide on,
	// and retired skill tools without a successor.
	Dropped []string
}

// CheckAllowedTools validates a requested allowed_tools list against the
// selectable tools. stored is the list currently saved for the agent, nil for
// a new agent.
func CheckAllowedTools(stored, requested []string) AllowedToolsCheck {
	if requested == nil {
		return AllowedToolsCheck{}
	}
	selectable := make(map[string]struct{})
	for _, tool := range AvailableToolDefinitions() {
		selectable[tool.Name] = struct{}{}
	}
	dropped := make(map[string]struct{})
	for _, name := range SwitchManagedTools() {
		dropped[name] = struct{}{}
	}
	for _, name := range []string{LegacyToolReadSkill, LegacyToolExecuteSkillScript, LegacyToolReadSandboxFile} {
		dropped[name] = struct{}{}
	}
	already := make(map[string]struct{}, len(stored))
	for _, name := range NormalizeAllowedTools(stored) {
		already[name] = struct{}{}
	}

	check := AllowedToolsCheck{Tools: []string{}}
	for _, name := range NormalizeAllowedTools(requested) {
		if _, ok := dropped[name]; ok {
			check.Dropped = append(check.Dropped, name)
			continue
		}
		if _, ok := selectable[name]; !ok {
			if _, kept := already[name]; !kept {
				check.Rejected = append(check.Rejected, name)
				continue
			}
			check.KeptUnknown = append(check.KeptUnknown, name)
		}
		check.Tools = append(check.Tools, name)
	}
	return check
}
