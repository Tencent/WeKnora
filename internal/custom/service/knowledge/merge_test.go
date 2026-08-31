package knowledge

import "testing"

func TestMapSkillToKnowledgeTypeSupportsSkillOntology(t *testing.T) {
	tests := map[string]KnowledgeType{
		"methodology":  TypeMethod,
		"case":         TypeCase,
		"concept":      TypeConcept,
		"insight":      TypeInsight,
		"entity":       TypeEntity,
		"person":       TypeEntity,
		"organization": TypeEntity,
		"product":      TypeEntity,
		"technology":   TypeEntity,
		"industry":     TypeEntity,
		"place":        TypeEntity,
	}
	for input, want := range tests {
		if got := MapSkillToKnowledgeType(input); got != want {
			t.Fatalf("MapSkillToKnowledgeType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMergeAnchorsDropsUnsupportedTypes(t *testing.T) {
	merged := MergeAnchors([]AnchorItem{
		{ID: "entity-1", Type: TypeEntity},
		{ID: "unknown-1", Type: KnowledgeType("unknown")},
	}, nil)

	if len(merged[TypeEntity]) != 1 || merged[TypeEntity][0].ID != "entity-1" {
		t.Fatalf("entity anchors = %#v", merged[TypeEntity])
	}
	if _, ok := merged[KnowledgeType("unknown")]; ok {
		t.Fatalf("unsupported type should not be returned: %#v", merged)
	}
}
