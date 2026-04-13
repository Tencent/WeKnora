package agent

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestJSONToolArgumentsSemanticallyEqual_userExample(t *testing.T) {
	a := `{
    "req": [
        {
            "knowledge_id": "fbd587fe-6249-448e-b174-d5818af5b42f",
            "limit": 20,
            "offset": 0
        },
        {
            "knowledge_id": "a6e73511-c239-4c35-b2ca-a78b4354e5e5",
            "limit": 20,
            "offset": 0
        },
        {
            "knowledge_id": "06bc4b39-3118-48e9-b4cc-ff8cc1e618d2",
            "limit": 20,
            "offset": 0
        },
        {
            "knowledge_id": "65fb8383-99e6-4b2f-b711-130c5a6dd4aa",
            "limit": 20,
            "offset": 0
        },
        {
            "knowledge_id": "cdb87e7f-2a47-4394-8a6f-ae0bc75e9969",
            "limit": 20,
            "offset": 0
        }
    ]
}`
	b := `{
  "req" : [ {
    "knowledge_id" : "a6e73511-c239-4c35-b2ca-a78b4354e5e5",
    "limit" : 20,
    "offset" : 0
  }, {
    "knowledge_id" : "fbd587fe-6249-448e-b174-d5818af5b42f",
    "limit" : 20,
    "offset" : 0
  }, {
    "knowledge_id" : "06bc4b39-3118-48e9-b4cc-ff8cc1e618d2",
    "limit" : 20,
    "offset" : 0
  }, {
    "knowledge_id" : "65fb8383-99e6-4b2f-b711-130c5a6dd4aa",
    "limit" : 20,
    "offset" : 0
  }, {
    "knowledge_id" : "cdb87e7f-2a47-4394-8a6f-ae0bc75e9969",
    "limit" : 20,
    "offset" : 0
  } ]
}`

	if !JSONToolArgumentsSemanticallyEqual(a, b) {
		t.Fatal("expected semantically equal JSON (format differs)")
	}
}

func TestFindDuplicateToolCallInMessages(t *testing.T) {
	messages := []chat.Message{
		{Role: "user", Content: "hi"},
		{
			Role: "assistant",
			ToolCalls: []chat.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: chat.FunctionCall{
						Name:      "list_knowledge_chunks",
						Arguments: `{"req":[{"knowledge_id":"x","limit":20,"offset":0}]}`,
					},
				},
			},
		},
	}

	msgIdx, tcIdx, ok := FindDuplicateToolCallInMessages(
		messages,
		"list_knowledge_chunks",
		`{
		  "req" : [ {
		    "knowledge_id" : "x",
		    "limit" : 20,
		    "offset" : 0
		  } ]
		}`,
	)
	if !ok {
		t.Fatal("expected duplicate in assistant history")
	}
	if msgIdx != 1 || tcIdx != 0 {
		t.Fatalf("unexpected hit index: msg=%d tc=%d", msgIdx, tcIdx)
	}
}

func TestFindDuplicateToolCallInMessages_notDuplicate(t *testing.T) {
	messages := []chat.Message{
		{
			Role: "assistant",
			ToolCalls: []chat.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: chat.FunctionCall{
						Name:      "knowledge_search",
						Arguments: `{"query":"a"}`,
					},
				},
			},
		},
	}
	_, _, ok := FindDuplicateToolCallInMessages(messages, "knowledge_search", `{"query":"b"}`)
	if ok {
		t.Fatal("expected non-duplicate when args differ")
	}
}

func TestFindDuplicateToolCallInCurrentResponseBeforeIndex(t *testing.T) {
	toolCalls := []types.LLMToolCall{
		{Function: types.FunctionCall{Name: "knowledge_search", Arguments: `{"query":"a"}`}},
		{Function: types.FunctionCall{Name: "knowledge_search", Arguments: `{"query":"a"}`}},
		{Function: types.FunctionCall{Name: "knowledge_search", Arguments: `{"query":"b"}`}},
	}
	prev, ok := FindDuplicateToolCallInCurrentResponseBeforeIndex(toolCalls, 1)
	if !ok || prev != 0 {
		t.Fatalf("expected duplicate at index 0, got prev=%d ok=%v", prev, ok)
	}
	if _, ok := FindDuplicateToolCallInCurrentResponseBeforeIndex(toolCalls, 2); ok {
		t.Fatal("index 2 should not be duplicate")
	}
}

func TestBuildDuplicateToolCallWarningCN(t *testing.T) {
	msg := BuildDuplicateToolCallWarningCN("knowledge_search", 3, 1)
	if msg == "" {
		t.Fatal("warning should not be empty")
	}
	if msg[0] != '[' {
		t.Fatal("warning format seems broken")
	}
}
