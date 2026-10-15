package operations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogScopeIdentifiersAreHandled(t *testing.T) {
	for _, op := range Builtin().All() {
		for _, param := range op.Params {
			if !IsScopeIdentifier(param.Name) {
				continue
			}
			kind, ok := op.ScopeParam(param.Name)
			require.True(t, ok, "%s.%s needs a scope rule", op.ID, param.Name)
			if kind == ScopeParamChild || kind == ScopeParamWiki {
				kb, ok := op.Param("knowledge_base_id")
				require.True(t, ok, op.ID)
				require.True(t, kb.Required, op.ID)
			}
		}
	}
	for id, params := range scopeParamExceptions {
		op, ok := Builtin().Get(id)
		require.True(t, ok, id)
		for name := range params {
			_, ok := op.Param(name)
			require.True(t, ok, id+"."+name)
		}
		require.True(t, !op.Risk.Mutates() || id == "agent.update", id)
	}
}
