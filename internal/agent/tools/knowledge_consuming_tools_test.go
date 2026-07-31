package tools

import "testing"

func TestKnowledgeConsumingToolsMatchCapabilitiesAndDefinitions(t *testing.T) {
	for name, requirement := range ToolCapabilityRequirements {
		requiresKnowledge := len(requirement.AnyOf) > 0 ||
			len(requirement.AllOf) > 0
		listed := IsKnowledgeConsumingTool(name)
		if listed != requiresKnowledge {
			t.Errorf(
				"IsKnowledgeConsumingTool(%q) = %t, capability requirement = %t",
				name,
				listed,
				requiresKnowledge,
			)
		}
	}

	available := make(map[string]struct{})
	for _, definition := range AvailableToolDefinitions() {
		available[definition.Name] = struct{}{}
	}
	for name := range knowledgeConsumingToolNames {
		requirement, exists := ToolCapabilityRequirements[name]
		if !exists ||
			(len(requirement.AnyOf) == 0 && len(requirement.AllOf) == 0) {
			t.Errorf(
				"knowledge-consuming tool %q has no KB capability requirement",
				name,
			)
		}
		if _, exists := available[name]; !exists {
			t.Errorf(
				"knowledge-consuming tool %q is missing from available definitions",
				name,
			)
		}
	}
}
