package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPageActionRequestValidate(t *testing.T) {
	for name, tc := range map[string]struct {
		req     PageActionPromptRequest
		wantErr string
	}{
		"whole page": {req: PageActionPromptRequest{Action: PageActionRead}},
		"field lines": {
			req: PageActionPromptRequest{Action: PageActionRead, Ref: "e5", StartLine: 10, LineCount: 50},
		},
		"bad ref": {
			req:     PageActionPromptRequest{Action: PageActionRead, Ref: "#title"},
			wantErr: "field reference",
		},
		"lines need a ref": {req: PageActionPromptRequest{Action: PageActionRead, StartLine: 3}, wantErr: "need a ref"},
		"too many lines": {
			req:     PageActionPromptRequest{Action: PageActionRead, Ref: "e1", LineCount: PageReadMaxLineCount + 1},
			wantErr: "line_count",
		},
		"unknown action": {req: PageActionPromptRequest{Action: "click"}, wantErr: "unknown page action"},
		"set and patch": {req: PageActionPromptRequest{Action: PageActionFill, Changes: []PageFillChange{
			{Ref: "e1", Value: json.RawMessage(`"New title"`)},
			{Ref: "e2", Value: json.RawMessage(`true`)},
			{Ref: "e3", Value: json.RawMessage(`["A","B"]`)},
			{Ref: "e4", Edits: []PageTextEdit{
				{Op: PageEditReplace, OldText: "old", Text: ""},
				{Op: PageEditInsertAfter, Anchor: "## Intro", Text: "\nmore"},
				{Op: PageEditReplaceSelection, Text: "x"},
				{Op: PageEditAppend, Text: "end"},
			}},
		}}},
		"empty fill": {req: PageActionPromptRequest{Action: PageActionFill}, wantErr: "empty"},
		"value and edits": {req: PageActionPromptRequest{Action: PageActionFill, Changes: []PageFillChange{
			{Ref: "e1", Value: json.RawMessage(`"x"`), Edits: []PageTextEdit{{Op: PageEditAppend, Text: "y"}}},
		}}, wantErr: "either value or edits"},
		"neither": {req: PageActionPromptRequest{Action: PageActionFill, Changes: []PageFillChange{
			{Ref: "e1", Value: json.RawMessage(`null`)},
		}}, wantErr: "either value or edits"},
		"same field twice": {req: PageActionPromptRequest{Action: PageActionFill, Changes: []PageFillChange{
			{Ref: "e1", Value: json.RawMessage(`"a"`)}, {Ref: "e1", Value: json.RawMessage(`"b"`)},
		}}, wantErr: "changed twice"},
		"object value": {req: PageActionPromptRequest{Action: PageActionFill, Changes: []PageFillChange{
			{Ref: "e1", Value: json.RawMessage(`{"a":1}`)},
		}}, wantErr: "value must be"},
		"replace without old text": {req: PageActionPromptRequest{Action: PageActionFill, Changes: []PageFillChange{
			{Ref: "e1", Edits: []PageTextEdit{{Op: PageEditReplace, Text: "x"}}},
		}}, wantErr: "old_text"},
		"insert without anchor": {req: PageActionPromptRequest{Action: PageActionFill, Changes: []PageFillChange{
			{Ref: "e1", Edits: []PageTextEdit{{Op: PageEditInsertBefore, Text: "x"}}},
		}}, wantErr: "anchor"},
		"too long": {req: PageActionPromptRequest{Action: PageActionFill, Changes: []PageFillChange{
			{Ref: "e1", Edits: []PageTextEdit{{Op: PageEditAppend, Text: strings.Repeat("字", PageFillMaxTextRunes+1)}}},
		}}, wantErr: "exceeds"},
		"navigate": {req: PageActionPromptRequest{
			Action: PageActionNavigate,
			Target: &PageNavigateTarget{Page: "settings", Section: "general"},
		}},
		"navigate nowhere": {req: PageActionPromptRequest{Action: PageActionNavigate}, wantErr: "needs a target"},
		"navigate with changes": {req: PageActionPromptRequest{
			Action:  PageActionNavigate,
			Target:  &PageNavigateTarget{Page: "agents"},
			Changes: []PageFillChange{{Ref: "e1", Value: json.RawMessage(`"x"`)}},
		}, wantErr: "only a target"},
		"read with a target": {req: PageActionPromptRequest{
			Action: PageActionRead,
			Target: &PageNavigateTarget{Page: "agents"},
		}, wantErr: "no changes or target"},
		"select a tab":         {req: PageActionPromptRequest{Action: PageActionSelect, Ref: "e3"}},
		"select without a ref": {req: PageActionPromptRequest{Action: PageActionSelect}, wantErr: "like e12"},
		"select with a change": {req: PageActionPromptRequest{
			Action:  PageActionSelect,
			Ref:     "e3",
			Changes: []PageFillChange{{Ref: "e1", Value: json.RawMessage(`"x"`)}},
		}, wantErr: "only a ref"},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.req.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestValidatePageActionResponse(t *testing.T) {
	read := json.RawMessage(`{"action":"read"}`)
	fill := json.RawMessage(`{"action":"fill","changes":[{"ref":"e1","value":"x"}]}`)

	require.NoError(t, ValidatePageActionResponse(read,
		json.RawMessage(`{"location":"KB","snapshot":"heading \"Docs\""}`)))
	require.NoError(t, ValidatePageActionResponse(read, json.RawMessage(`{"declined":true}`)))
	require.NoError(t, ValidatePageActionResponse(fill,
		json.RawMessage(`{"applied":[{"ref":"e1","label":"Title","summary":"set to x"}]}`)))
	require.NoError(t, ValidatePageActionResponse(fill, json.RawMessage(`{"error":"e1 is gone"}`)))

	assert.Error(t, ValidatePageActionResponse(read, json.RawMessage(`{"snapshot":"x","extra":1}`)),
		"unknown fields are rejected")
	assert.Error(t, ValidatePageActionResponse(read, json.RawMessage(`{"applied":[{"ref":"e1"}]}`)))
	assert.Error(t, ValidatePageActionResponse(fill, json.RawMessage(`{"snapshot":"x"}`)))
	assert.Error(t, ValidatePageActionResponse(fill,
		json.RawMessage(`{"error":"x","applied":[{"ref":"e1"}]}`)), "a failed fill applies nothing")
	assert.Error(t, ValidatePageActionResponse(fill,
		json.RawMessage(`{"applied":[{"ref":"e1"},{"ref":"e2"}]}`)), "no more fields than requested")
	assert.Error(t, ValidatePageActionResponse(read, json.RawMessage(`{"declined":true,"snapshot":"x"}`)))
	big, _ := json.Marshal(PageActionPromptResponse{Snapshot: strings.Repeat("a", PageSnapshotMaxRunes+1)})
	assert.Error(t, ValidatePageActionResponse(read, big))

	nav := json.RawMessage(`{"action":"navigate","target":{"page":"settings","section":"general"}}`)
	require.NoError(t, ValidatePageActionResponse(nav,
		json.RawMessage(`{"location":"Settings","snapshot":"x","return_path":"/platform/agents?x=1"}`)))
	require.NoError(t, ValidatePageActionResponse(nav, json.RawMessage(`{"stayed":true}`)))
	for _, bad := range []string{
		`{"stayed":true,"snapshot":"x"}`,
		`{"return_path":"https://evil.example"}`,
		`{"return_path":"//evil.example"}`,
		`{"applied":[{"ref":"e1"}]}`,
	} {
		assert.Error(t, ValidatePageActionResponse(nav, json.RawMessage(bad)), bad)
	}
	assert.Error(t, ValidatePageActionResponse(read, json.RawMessage(`{"stayed":true}`)), "only a navigate stays")

	sel := json.RawMessage(`{"action":"select","ref":"e3"}`)
	require.NoError(t, ValidatePageActionResponse(sel,
		json.RawMessage(`{"location":"Settings","snapshot":"tab \"用户管理\" [selected]"}`)))
	assert.Error(t, ValidatePageActionResponse(sel, json.RawMessage(`{"applied":[{"ref":"e3"}]}`)))
}
