package tools

// AgentToolRequirement is the JSON form of a tool's ToolRequirement: the
// knowledge base capabilities the agent's scope must expose for the tool to
// be registered.
type AgentToolRequirement struct {
	AnyOf []KBCapability `json:"any_of,omitempty"`
	AllOf []KBCapability `json:"all_of,omitempty"`
}

// AgentToolInfo is a selectable tool together with its capability
// requirement.
type AgentToolInfo struct {
	AvailableTool
	Requirement AgentToolRequirement `json:"requirement"`
}

// AgentToolCatalog is everything the agent editor needs to show an
// allowed_tools list the way the runtime reads it.
type AgentToolCatalog struct {
	// Tools are the selectable tools, in display order.
	Tools []AgentToolInfo `json:"tools"`
	// DefaultTools apply when an agent's allowed_tools was never set (null).
	DefaultTools []string `json:"default_tools"`
	// SwitchManagedTools follow other agent settings; naming them in the list
	// does not register them.
	SwitchManagedTools []string `json:"switch_managed_tools"`
}

// BuildAgentToolCatalog assembles the catalog from the tool definitions and
// the capability map, so the editor never keeps its own copy of either.
func BuildAgentToolCatalog() AgentToolCatalog {
	definitions := AvailableToolDefinitions()
	infos := make([]AgentToolInfo, 0, len(definitions))
	for _, definition := range definitions {
		requirement := ToolCapabilityRequirements[definition.Name]
		infos = append(infos, AgentToolInfo{
			AvailableTool: definition,
			// Copies, so a caller cannot reach into the shared capability map.
			Requirement: AgentToolRequirement{
				AnyOf: append([]KBCapability(nil), requirement.AnyOf...),
				AllOf: append([]KBCapability(nil), requirement.AllOf...),
			},
		})
	}
	return AgentToolCatalog{
		Tools:              infos,
		DefaultTools:       DefaultAllowedTools(),
		SwitchManagedTools: SwitchManagedTools(),
	}
}
