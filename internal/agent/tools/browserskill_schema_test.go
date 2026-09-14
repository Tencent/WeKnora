package tools

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/browserskill"
	"github.com/stretchr/testify/require"
)

func TestBrowserFlatSchemaCoversEveryMethod(t *testing.T) {
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	require.NoError(t, json.Unmarshal(tool.Parameters(), &schema))
	require.NotContains(t, schema.Properties, "params")
	require.NotContains(t, schema.Properties, "session_id")
	require.Equal(t, []string{"method"}, schema.Required)
	var method struct {
		Enum []string `json:"enum"`
	}
	require.NoError(t, json.Unmarshal(schema.Properties["method"], &method))
	valid := map[string]string{
		"screenshot": `{"ref":"e3","tab_id":1}`,
		"observe":    `{}`, "snapshot": `{}`, "navigate": `{"url":"https://example.com"}`,
		"navigate_back": `{}`, "navigate_forward": `{}`, "reload": `{}`,
		"click": `{"ref":"e1"}`, "fill": `{"selector":"input","value":""}`,
		"press": `{"key":"Enter"}`, "hover": `{"ref":"e1"}`, "wheel": `{"delta_y":500}`,
		"scroll_to": `{"ref":"e1"}`, "focus": `{"ref":"e1"}`, "blur": `{}`,
		"select": `{"ref":"e1","values":[]}`, "tab_list": `{"scope":"agent"}`,
		"tab_create": `{}`, "tab_select": `{"tab_id":3}`, "tab_close": `{"tab_id":3}`,
		"tab_borrow": `{"tab_id":3}`, "tab_return": `{"tab_id":3}`, "get_html": `{}`,
		"evaluate": `{"expression":"document.title"}`, "console": `{}`, "network": `{}`,
		"wait_for_navigation": `{}`, "wait_ms": `{"duration_ms":0}`,
		"window_resize": `{"width":800,"height":600}`, "emulate": `{"off":true}`,
		"request_help": `{"prompt":"Please sign in"}`,
	}
	require.Len(t, valid, len(method.Enum))
	require.Len(t, browserArgumentRules, len(method.Enum))
	for _, name := range method.Enum {
		t.Run(name, func(t *testing.T) {
			fields, exists := valid[name]
			require.True(t, exists)
			var input map[string]any
			require.NoError(t, json.Unmarshal([]byte(fields), &input))
			input["method"] = name
			raw, err := json.Marshal(input)
			require.NoError(t, err)
			require.NoError(t, tool.ValidateArguments(raw))
			for _, field := range browserArgumentRules[name].fields {
				require.Contains(t, schema.Properties, field)
			}
		})
	}
}

func TestBrowserFlatArgumentsRejectBeforeDispatch(t *testing.T) {
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat")
	for _, raw := range []string{
		`{"method":"observe","keep_open":"true"}`,
		`null`, `[]`, `{}`, `{"method":"unknown"}`,
		`{"method":"navigate","params":{"url":"https://example.com"}}`,
		`{"method":"navigate","url":"https://example.com","params":{}}`,
		`{"method":"navigate"}`, `{"method":"navigate","url":""}`,
		`{"method":"navigate","url":123}`, `{"method":"navigate","url":"https://example.com","session_id":"foreign"}`,
		`{"method":"observe","browser_instance_id":"foreign"}`,
		`{"method":"click","url":"https://example.com"}`, `{"method":"click"}`,
		`{"method":"click","ref":"e1","selector":"button"}`, `{"method":"click","ref":""}`,
		`{"method":"fill","ref":"e1"}`, `{"method":"press"}`,
		`{"method":"select","ref":"e1","values":[1]}`,
		`{"method":"click","ref":"e1","modifiers":["unknown"]}`,
		`{"method":"tab_select"}`, `{"method":"tab_select","tab_id":0.5}`,
		`{"method":"wait_ms","duration_ms":10001}`, `{"method":"wait_ms","duration_ms":"100"}`,
		`{"method":"window_resize","width":800}`, `{"method":"window_resize","width":1,"height":600}`,
		`{"method":"emulate"}`, `{"method":"emulate","off":true,"overrides":{"width":800}}`,
		`{"method":"emulate","overrides":"{}"}`, `{"method":"request_help","prompt":null}`,
	} {
		t.Run(raw, func(t *testing.T) { require.Error(t, tool.ValidateArguments(json.RawMessage(raw))) })
	}
	require.NoError(t, tool.ValidateArguments(
		json.RawMessage(`{"method":"emulate","overrides":{"width":800,"height":600}}`),
	))
	require.NoError(t, tool.ValidateArguments(
		json.RawMessage(`{"method":"navigate","url":"https://example.com","wait_until":"load"}`),
	))
}
