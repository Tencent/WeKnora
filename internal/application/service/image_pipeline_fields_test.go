package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// fieldGroupRank orders the three runs a pipeline declares its fields in. The
// panel renders them as three places — always-visible switches, a folded
// "advanced" block, a folded "notes" block — so a field declared out of run
// would silently appear somewhere else on screen.
func fieldGroupRank(field types.ImageFieldDef) int {
	switch field.Group {
	case types.ImageFieldGroupAdvanced:
		return 1
	case types.ImageFieldGroupNote:
		return 2
	default:
		return 0
	}
}

// TestFieldDeclarationOrder holds every registered pipeline to the declaration
// contract: standing switches first, then advanced switches, then notes, each
// run in the order the panel should show it. See the Fields() doc on the
// imagePipeline interface — the order is what makes the panel's layout a
// property of the pipeline rather than of the renderer.
func TestFieldDeclarationOrder(t *testing.T) {
	for id, pipeline := range imagePipelineRegistry {
		last := 0
		seen := make(map[string]bool)
		for _, field := range pipeline.Fields() {
			if field.Key == "" {
				t.Errorf("pipeline %s declares a field with no key", id)
				continue
			}
			if seen[field.Key] {
				t.Errorf("pipeline %s declares the key %q twice", id, field.Key)
			}
			seen[field.Key] = true

			rank := fieldGroupRank(field)
			if rank < last {
				t.Errorf("pipeline %s declares %q out of run: fields must be declared "+
					"as standing switches, then advanced switches, then notes",
					id, field.Key)
			}
			last = rank

			// The note run is for text and the switch runs are for controls, so
			// the two markers have to agree: a control in the note run would
			// render as a blank row, and a note among the switches as a switch
			// that reads nothing.
			isNote := field.Type == types.ImageFieldTypeNote
			if isNote != (field.Group == types.ImageFieldGroupNote) {
				t.Errorf("pipeline %s: field %q has type %q in group %q; a note is the "+
					"only thing the note run holds", id, field.Key, field.Type, field.Group)
			}
		}
	}
}
