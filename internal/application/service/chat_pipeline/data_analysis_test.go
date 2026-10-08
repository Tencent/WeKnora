package chatpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// scriptedChat returns one canned response per call and records the prompts.
type scriptedChat struct {
	replies []string
	calls   [][]chat.Message
}

func (c *scriptedChat) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	c.calls = append(c.calls, messages)
	if len(c.replies) == 0 {
		return nil, errors.New("no scripted reply left")
	}
	reply := c.replies[0]
	c.replies = c.replies[1:]
	return &types.ChatResponse{Content: reply}, nil
}

func (c *scriptedChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not used")
}
func (c *scriptedChat) GetModelName() string { return "scripted" }
func (c *scriptedChat) GetModelID() string   { return "scripted" }

// scriptedExecutor fails for every SQL listed in rejects and records inputs.
type scriptedExecutor struct {
	rejects map[string]string // sql -> error text
	inputs  []tools.DataAnalysisInput
}

func (e *scriptedExecutor) Execute(_ context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var input tools.DataAnalysisInput
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	e.inputs = append(e.inputs, input)
	if msg, rejected := e.rejects[input.SQL]; rejected {
		return &types.ToolResult{Success: false, Error: msg}, errors.New(msg)
	}
	return &types.ToolResult{Success: true, Output: "rows for " + input.SQL}, nil
}

const testKnowledgeID = "3f1c2a8e-0000-4000-8000-000000000001"

var testSchema = &tools.TableSchema{
	TableName: "k_3f1c2a8e_0000_4000_8000_000000000001",
	Columns:   []tools.ColumnInfo{{Name: "amount", Type: "VARCHAR"}},
	RowCount:  3,
}

func TestRunDataAnalysisNotNeededReturnsNothing(t *testing.T) {
	model := &scriptedChat{replies: []string{`{"needs_analysis": false, "sql": ""}`}}
	executor := &scriptedExecutor{}
	result, err := runDataAnalysis(context.Background(), model, executor, testKnowledgeID, "hello", testSchema)
	if err != nil || result != nil {
		t.Fatalf("not-needed plan must yield (nil, nil), got (%v, %v)", result, err)
	}
	if len(executor.inputs) != 0 {
		t.Fatal("executor must not run without SQL")
	}
}

func TestRunDataAnalysisPipelineOwnsDocumentSelection(t *testing.T) {
	model := &scriptedChat{replies: []string{
		"```json\n{\"needs_analysis\": true, \"sql\": \"SELECT COUNT(*) FROM dataset\"}\n```",
	}}
	executor := &scriptedExecutor{}
	result, err := runDataAnalysis(context.Background(), model, executor, testKnowledgeID, "how many rows", testSchema)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("expected success, got (%+v, %v)", result, err)
	}
	if len(executor.inputs) != 1 || executor.inputs[0].KnowledgeID != testKnowledgeID ||
		executor.inputs[0].SQL != "SELECT COUNT(*) FROM dataset" {
		t.Fatalf("executor input = %+v", executor.inputs)
	}
	prompt := model.calls[0][0].Content
	if !strings.Contains(prompt, `"dataset"`) {
		t.Fatalf("prompt must name the fixed table:\n%s", prompt)
	}
	if strings.Contains(prompt, testKnowledgeID) || strings.Contains(prompt, testSchema.TableName) {
		t.Fatalf("prompt must not expose the knowledge ID or physical table:\n%s", prompt)
	}
}

func TestRunDataAnalysisRepairsRejectedSQLOnce(t *testing.T) {
	model := &scriptedChat{replies: []string{
		`{"needs_analysis": true, "sql": "SELECT * FROM k_3f1c2a8e_0000_4000_8000_000000000001"}`,
		`{"needs_analysis": true, "sql": "SELECT * FROM dataset"}`,
	}}
	executor := &scriptedExecutor{rejects: map[string]string{
		"SELECT * FROM k_3f1c2a8e_0000_4000_8000_000000000001": "Table 'k_...' is not in the allowed list. " +
			`Use "dataset"`,
	}}
	result, err := runDataAnalysis(context.Background(), model, executor, testKnowledgeID, "show all", testSchema)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("repaired query must succeed, got (%+v, %v)", result, err)
	}
	if len(executor.inputs) != 2 || len(model.calls) != 2 {
		t.Fatalf("expected one retry: executor=%d model=%d", len(executor.inputs), len(model.calls))
	}
	repair := model.calls[1]
	if len(repair) != 3 || repair[1].Role != "assistant" || repair[2].Role != "user" {
		t.Fatalf("repair turn must replay the failed plan then the error: %+v", repair)
	}
	if !strings.Contains(repair[2].Content, "not in the allowed list") ||
		!strings.Contains(repair[2].Content, `"dataset"`) {
		t.Fatalf("repair prompt must carry the tool error and the table name:\n%s", repair[2].Content)
	}
}

func TestRunDataAnalysisSurfacesPersistentFailure(t *testing.T) {
	model := &scriptedChat{replies: []string{
		`{"needs_analysis": true, "sql": "SELECT nope FROM dataset"}`,
		`{"needs_analysis": true, "sql": "SELECT nope FROM dataset"}`,
	}}
	executor := &scriptedExecutor{rejects: map[string]string{
		"SELECT nope FROM dataset": `Referenced column "nope" not found`,
	}}
	result, err := runDataAnalysis(context.Background(), model, executor, testKnowledgeID, "q", testSchema)
	if err == nil || result != nil {
		t.Fatalf("a query that fails twice must be reported, got (%v, %v)", result, err)
	}
	if !strings.Contains(err.Error(), "attempt 2") || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("error must name the last attempt and its cause: %v", err)
	}
	if len(executor.inputs) != 2 {
		t.Fatalf("exactly two attempts expected, got %d", len(executor.inputs))
	}
}

func TestRunDataAnalysisRejectsUnparseablePlan(t *testing.T) {
	model := &scriptedChat{replies: []string{"I think you should look at the data"}}
	executor := &scriptedExecutor{}
	if _, err := runDataAnalysis(context.Background(), model, executor, testKnowledgeID, "q", testSchema); err == nil {
		t.Fatal("prose instead of JSON must be an error, not silence")
	}
	if len(executor.inputs) != 0 {
		t.Fatal("executor must not run on an unparseable plan")
	}
}
