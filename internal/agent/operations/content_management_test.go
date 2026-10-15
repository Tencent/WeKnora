package operations

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWikiFolderMutationsRequireConfirmationAndEmptyDeleteProjects(t *testing.T) {
	for _, name := range []string{"wiki.update_folder", "wiki.delete_folder"} {
		op, ok := Builtin().Get(name)
		require.True(t, ok)
		require.Equal(t, RiskDestructive, op.Risk)
		require.Equal(t, "wiki.get_folder", op.Target.Operation)
	}
	op, _ := Builtin().Get("wiki.delete_folder")
	result, err := op.Project(nil)
	require.NoError(t, err)
	require.Empty(t, result.Data)
	ordinary, _ := Builtin().Get("wiki.get_folder")
	_, err = ordinary.Project(nil)
	require.Error(t, err)
}

func TestFAQSelectedFieldsAreBoundedAndNotBulkByTag(t *testing.T) {
	op, ok := Builtin().Get("faq.update_selected_fields")
	require.True(t, ok)
	_, err := op.Build(map[string]json.RawMessage{
		"knowledge_base_id": raw("kb"), "entry_ids": raw([]int{1}), "is_enabled": raw(false),
	})
	require.NoError(t, err)
	ids := make([]int, 21)
	for i := range ids {
		ids[i] = i + 1
	}
	_, err = op.Build(map[string]json.RawMessage{
		"knowledge_base_id": raw("kb"), "entry_ids": raw(ids), "is_enabled": raw(false),
	})
	require.Error(t, err)
	_, ok = op.Param("by_tag")
	require.False(t, ok)
}

func TestSelectedAttachmentOperationsDoNotAcceptPathsOrSessions(t *testing.T) {
	for _, name := range []string{"knowledge.create_from_attachment", "faq.import_selected"} {
		op, ok := Builtin().Get(name)
		require.True(t, ok)
		params := map[string]json.RawMessage{"knowledge_base_id": raw("kb"), "attachment_id": raw("selected")}
		_, err := op.Build(params)
		require.NoError(t, err)
		for _, key := range []string{"session_id", "url", "path", "mode"} {
			params[key] = raw("untrusted")
			_, err = op.Build(params)
			require.Error(t, err)
			delete(params, key)
		}
	}
	op, _ := Builtin().Get("faq.import_selected")
	require.Equal(t, RiskDestructive, op.Risk)
}
