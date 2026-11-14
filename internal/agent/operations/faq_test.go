package operations

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func faqParams() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"knowledge_base_id": raw("kb1"), "standard_question": raw("Question"),
		"similar_questions": raw([]string{}), "negative_questions": raw([]string{}),
		"answers": raw([]string{"Answer"}), "answer_strategy": raw("all"), "tag_seq_id": raw(0),
	}
}

func TestFAQRequiredListsAllowEmptyButNotOmitted(t *testing.T) {
	for _, name := range []string{"faq.create_entry", "faq.update_entry"} {
		t.Run(name, func(t *testing.T) {
			op, ok := Builtin().Get(name)
			require.True(t, ok)
			params := faqParams()
			if name == "faq.update_entry" {
				params["entry_id"] = raw(7)
			}
			call, err := op.Build(params)
			require.NoError(t, err)
			var body map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(call.Body, &body))
			for _, field := range []string{"similar_questions", "negative_questions"} {
				assert.JSONEq(t, `[]`, string(body[field]))
				delete(params, field)
				_, err = op.Build(params)
				require.ErrorContains(t, err, field+": is required")
				params[field] = raw(nil)
				_, err = op.Build(params)
				require.ErrorContains(t, err, field+": is required")
				params[field] = raw([]string{})
			}
			params["standard_question"] = raw("")
			_, err = op.Build(params)
			require.ErrorContains(t, err, "must not be empty")
		})
	}
	for _, tc := range []struct{ name, field string }{
		{"knowledge.batch_delete", "knowledge_ids"},
		{"faq.delete_entries", "entry_ids"},
		{"faq.add_similar_questions", "similar_questions"},
	} {
		op, _ := Builtin().Get(tc.name)
		params := map[string]json.RawMessage{"knowledge_base_id": raw("kb1"), tc.field: raw([]string{})}
		if tc.name == "faq.add_similar_questions" {
			params["entry_id"] = raw(7)
		}
		_, err := op.Build(params)
		require.ErrorContains(t, err, "must not be empty", tc.name)
	}
}

func TestFAQReadThenRenamePreservesFullAnswers(t *testing.T) {
	get, _ := Builtin().Get("faq.get_entry")
	update, _ := Builtin().Get("faq.update_entry")
	require.True(t, update.Target.RequireComplete)
	for _, answer := range []string{strings.Repeat("a", 3000), strings.Repeat("文", 8000), strings.Repeat("😀", 8000)} {
		body := raw(map[string]any{"data": map[string]any{
			"id": 7, "standard_question": "Question", "similar_questions": []string{},
			"negative_questions": []string{}, "answers": []string{answer}, "answer_strategy": "all", "tag_id": 0,
		}})
		projected, err := get.Project(body)
		require.NoError(t, err)
		require.False(t, projected.Truncated)
		params := map[string]json.RawMessage{"knowledge_base_id": raw("kb1")}
		for k, v := range projected.Data.(map[string]any) {
			if _, ok := update.Param(k); ok {
				params[k] = raw(v)
			}
		}
		params["standard_question"] = raw("Renamed")
		call, err := update.Build(params)
		require.NoError(t, err)
		var stored struct {
			Answers []string `json:"answers"`
		}
		require.NoError(t, json.Unmarshal(call.Body, &stored))
		assert.Equal(t, []string{answer}, stored.Answers)
	}
}

func TestProjectionReportsTruncatedFieldsAndNestedLists(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     any
		truncated bool
	}{
		{"exact string limit", strings.Repeat("文", 2000), false},
		{"long string", strings.Repeat("文", 2001), true},
		{"long list item", []string{strings.Repeat("a", 2001)}, true},
		{"too many list items", make([]string, 101), true},
		{"exact list limit", make([]string, 100), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, result := range []Result{
				{Fields: []Field{{From: "value"}}},
				{Root: "rows", List: true, Fields: []Field{{From: "value"}}},
				{Root: "rows", List: true, Meta: []Field{{From: "value"}}},
			} {
				op := Operation{Result: result}
				body := raw(map[string]any{"value": tc.value, "rows": []any{map[string]any{"value": tc.value}}})
				projected, err := op.Project(body)
				require.NoError(t, err)
				assert.Equal(t, tc.truncated, projected.Truncated)
			}
		})
	}
}
