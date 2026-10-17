package agent

import (
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChatExcelUploadRegistersDocumentHandle is the regression for issue #4170.
// A spreadsheet uploaded in the chat window is a temporary document, not a
// knowledge-base row. The turn must still give data_schema / data_analysis a
// resolvable dN handle whose file_type is xlsx, while the knowledge-base PDF
// keeps its own handle and a hallucinated d26 stays unresolved.
func TestChatExcelUploadRegistersDocumentHandle(t *testing.T) {
	const excelID = "11111111-1111-4111-8111-111111111111"
	const pdfID = "22222222-2222-4222-8222-222222222222"

	engine := &AgentEngine{
		modelContext: modelcontext.NewRegistry(true),
		knowledgeBasesInfo: []*KnowledgeBaseInfo{{
			ID:   "kb-1",
			Name: "Docs",
			RecentDocs: []RecentDocInfo{{
				KnowledgeID: pdfID,
				Title:       "manual.pdf",
				FileName:    "manual.pdf",
				Type:        "pdf",
			}},
		}},
	}
	store := agenttools.NewChatTabularAttachments()
	store.Set([]agenttools.ChatTabularAttachment{
		{ID: excelID, FileName: "sales.xlsx", FileType: ".XLSX"},
		{ID: "33333333-3333-4333-8333-333333333333", FileName: "manual.pdf", FileType: "pdf"},
	})
	engine.BindChatAttachments(store)

	query := "统计这个 Excel 的每一列" + types.MessageAttachments{{
		ID:       excelID,
		FileName: "sales.xlsx",
		FileType: ".xlsx",
		FileSize: 128,
		Content:  "name,qty\nalice,1",
	}}.BuildPrompt()
	out := engine.RenderUserTurnContent("sess-1", query)

	assert.Contains(t, out, `<chat_uploads scope="this_turn">`)
	assert.Contains(t, out, `<document knowledge_id="d2" title="sales.xlsx" file_type="xlsx" />`)
	assert.Contains(t, out, "These CSV or Excel files were uploaded in the chat.")
	assert.Contains(t, out, `<document knowledge_id="d1" chunk_id="" type="pdf"><name>manual.pdf</name></document>`)
	assert.Contains(t, out, "<type>.xlsx</type>", "extracted attachment text stays in the user turn")
	assert.Contains(t, out, "name,qty")
	assert.NotContains(t, out, excelID, "the durable temporary-document id must be compacted to a handle")
	assert.NotContains(t, out, pdfID)
	assert.NotContains(t, out, "33333333-3333-4333-8333-333333333333", "a PDF upload is not a data-analysis document")
	assert.NotContains(t, out, `file_type="pdf"`)
	assert.Contains(t, runtimePromptContract, "CSV and Excel files uploaded in the chat are listed under chat_uploads")
	assert.Contains(t, runtimePromptContract, "including a PDF from a knowledge base")

	excelCall := []types.LLMToolCall{{
		Function: types.FunctionCall{Name: "data_schema", Arguments: `{"knowledge_id":"d2"}`},
	}}
	engine.modelContext.DecodeToolCalls(excelCall)
	assert.Contains(t, excelCall[0].Function.Arguments, excelID)
	assert.NotContains(t, excelCall[0].Function.Arguments, pdfID)
	assert.Empty(t, excelCall[0].UnresolvedHandles)

	pdfCall := []types.LLMToolCall{{
		Function: types.FunctionCall{Name: "data_analysis", Arguments: `{"knowledge_id":"d1"}`},
	}}
	engine.modelContext.DecodeToolCalls(pdfCall)
	assert.Contains(t, pdfCall[0].Function.Arguments, pdfID)
	assert.NotContains(t, pdfCall[0].Function.Arguments, excelID)
	assert.Empty(t, pdfCall[0].UnresolvedHandles)

	// act.go rejects a call before the tool runs when UnresolvedHandles is set.
	// d26 was the handle the model invented in issue #4170.
	invented := []types.LLMToolCall{{
		Function: types.FunctionCall{Name: "data_schema", Arguments: `{"knowledge_id":"d26"}`},
	}}
	engine.modelContext.DecodeToolCalls(invented)
	assert.Equal(t, []string{"d26"}, invented[0].UnresolvedHandles)
	assert.Contains(t, invented[0].Function.Arguments, "d26")
}

func TestChatUploadsOmittedWhenNoneAreTabular(t *testing.T) {
	engine := &AgentEngine{modelContext: modelcontext.NewRegistry(true)}
	engine.SetChatAttachments([]agenttools.ChatTabularAttachment{{
		ID: "33333333-3333-4333-8333-333333333333", FileName: "notes.pdf", FileType: ".pdf",
	}})
	out := engine.RenderUserTurnContent("sess-1", "hello")
	assert.NotContains(t, out, "<chat_uploads")
	require.NotContains(t, out, "33333333-3333-4333-8333-333333333333")
}

func TestWriteChatUploadsEscapesTitle(t *testing.T) {
	block := buildRuntimeContextBlock("sess-1", nil, nil, nil, nil, "", []ChatUploadInfo{{
		DocumentID: "doc-1",
		Title:      `Q3 <sales> & "forecast"`,
		FileType:   "csv",
	}})
	assert.Contains(t, block, `title="Q3 &lt;sales&gt; &amp; &quot;forecast&quot;"`)
	assert.Contains(t, block, `file_type="csv"`)
	assert.NotContains(t, block, "<sales>")
}
