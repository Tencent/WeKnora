package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAskUserRequestValidate(t *testing.T) {
	many := make([]string, AskUserMaxOptions+1)
	for i := range many {
		many[i] = strings.Repeat("o", i+1)
	}
	for name, tc := range map[string]struct {
		req AskUserPromptRequest
		err string
	}{
		"free text":      {req: AskUserPromptRequest{Question: " which? "}},
		"options":        {req: AskUserPromptRequest{Question: "q", Options: []string{"a", "b"}}},
		"empty question": {req: AskUserPromptRequest{Question: "  "}, err: "question is required"},
		"long question": {
			req: AskUserPromptRequest{Question: strings.Repeat("问", AskUserMaxQuestionLen+1)}, err: "exceeds",
		},
		"too many options": {req: AskUserPromptRequest{Question: "q", Options: many}, err: "at most"},
		"duplicate option": {req: AskUserPromptRequest{Question: "q", Options: []string{"a", "a"}}, err: "duplicate"},
		"options and fields": {req: AskUserPromptRequest{
			Question: "q", Options: []string{"a"}, Fields: []AskUserField{{Name: "n", Type: AskUserFieldText}},
		}, err: "either"},
		"unknown field type": {req: AskUserPromptRequest{
			Question: "q", Fields: []AskUserField{{Name: "n", Type: "date"}},
		}, err: "unsupported"},
		"select without options": {req: AskUserPromptRequest{
			Question: "q", Fields: []AskUserField{{Name: "n", Type: AskUserFieldSelect}},
		}, err: "needs options"},
		"duplicate field": {req: AskUserPromptRequest{
			Question: "q", Fields: []AskUserField{
				{Name: "n", Type: AskUserFieldText}, {Name: "n", Type: AskUserFieldNumber},
			},
		}, err: "duplicate field"},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.req.Validate()
			if tc.err == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}

func TestValidateAskUserResponse(t *testing.T) {
	form := `{"question":"q","fields":[
		{"name":"env","type":"select","options":["dev","prod"],"required":true},
		{"name":"replicas","type":"number"},
		{"name":"tags","type":"multiselect","options":["a","b"]},
		{"name":"note","type":"text"}]}`
	choice := `{"question":"q","options":["a","b"]}`
	for name, tc := range map[string]struct {
		request, response string
		err               string
	}{
		"free text answer":     {request: `{"question":"q"}`, response: `{"text":"blue"}`},
		"free text missing":    {request: `{"question":"q"}`, response: `{}`, err: "required"},
		"declined":             {request: choice, response: `{"declined":true}`},
		"declined with answer": {request: choice, response: `{"declined":true,"selected":["a"]}`, err: "no content"},
		"choice":               {request: choice, response: `{"selected":["b"]}`},
		"not an option":        {request: choice, response: `{"selected":["c"]}`, err: "not an option"},
		"two of single choice": {request: choice, response: `{"selected":["a","b"]}`, err: "only one"},
		"no choice":            {request: choice, response: `{}`, err: "choose"},
		"text not allowed":     {request: choice, response: `{"text":"x"}`, err: "free text"},
		"text allowed": {
			request:  `{"question":"q","options":["a"],"allow_free_text":true}`,
			response: `{"text":"other"}`,
		},
		"form": {
			request:  form,
			response: `{"values":{"env":"prod","replicas":3,"tags":["a"],"note":"ok"}}`,
		},
		"form missing required": {request: form, response: `{"values":{"replicas":3}}`, err: `"env" is required`},
		"form wrong type":       {request: form, response: `{"values":{"env":"prod","replicas":"3"}}`, err: "number"},
		"form unknown field":    {request: form, response: `{"values":{"env":"prod","x":1}}`, err: "unknown field"},
		"form bad option":       {request: form, response: `{"values":{"env":"staging"}}`, err: "not an option"},
		"unknown key":           {request: choice, response: `{"selected":["a"],"extra":1}`, err: "unknown field"},
		"long text": {
			request:  `{"question":"q"}`,
			response: `{"text":"` + strings.Repeat("x", AskUserMaxTextLen+1) + `"}`,
			err:      "exceeds",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateAskUserResponse(json.RawMessage(tc.request), json.RawMessage(tc.response))
			if tc.err == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}
