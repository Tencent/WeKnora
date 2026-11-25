package chatpipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
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

func TestPluginDataAnalysis_MergedTitleExcelEndToEnd(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	for _, ext := range []string{"spatial", "excel"} {
		if _, err := db.ExecContext(ctx, "INSTALL "+ext+"; LOAD "+ext+";"); err != nil {
			t.Skipf("cannot install/load duckdb %s extension: %v", ext, err)
		}
	}

	dir := t.TempDir()
	xlsxPath := filepath.Join(dir, "sales.xlsx")
	wb := excelize.NewFile()
	require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2026 年度销售统计"))
	require.NoError(t, wb.MergeCell("Sheet1", "A1", "D1"))
	for i, h := range []string{"月份", "地区", "销售额", "占比"} {
		cell, err := excelize.CoordinatesToCellName(i+1, 2)
		require.NoError(t, err)
		require.NoError(t, wb.SetCellValue("Sheet1", cell, h))
	}
	for r, row := range [][]any{
		{"1月", "华东", 100, 0.25},
		{"1月", "华南", 200, 0.50},
		{"2月", "华东", 100, 0.25},
	} {
		for c, val := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+3)
			require.NoError(t, err)
			require.NoError(t, wb.SetCellValue("Sheet1", cell, val))
		}
	}
	require.NoError(t, wb.SaveAs(xlsxPath))
	require.NoError(t, wb.Close())

	knowledge := &types.Knowledge{
		ID: testKnowledgeID, KnowledgeBaseID: "kb", TenantID: 7,
		FileType: "xlsx", FileName: "sales.xlsx", FilePath: xlsxPath,
	}
	model := &scriptedChat{replies: []string{
		`{"needs_analysis":true,"sql":"SELECT \"地区\", SUM(CAST(\"销售额\" AS DOUBLE PRECISION)) AS \"总额\" ` +
			`FROM dataset GROUP BY \"地区\" ORDER BY \"地区\""}`,
	}}
	plugin := NewPluginDataAnalysis(
		NewEventManager(), &stubModelService{model: model},
		&analysisStorageKBService{kb: &types.KnowledgeBase{ID: "kb", TenantID: 7}},
		&analysisStorageKnowledgeService{knowledge: knowledge},
		filesvc.NewLocalFileService(dir, ""), nil, nil, db, nil,
	)

	turn := &types.ChatManage{}
	turn.SessionID = "merged-xlsx-e2e"
	turn.Query = "按地区统计销售额总和"
	turn.MergeResult = []*types.SearchResult{{
		ID: "chunk-1", KnowledgeID: knowledge.ID, KnowledgeFilename: knowledge.FileName,
		ChunkType: string(types.ChunkTypeText), Content: "raw table excerpt",
	}}
	continued := false
	pluginErr := plugin.OnEvent(ctx, types.DATA_ANALYSIS, turn, func() *PluginError {
		continued = true
		return nil
	})
	require.Nil(t, pluginErr)
	require.True(t, continued)
	require.Nil(t, turn.DataAnalysisFailure)
	require.Len(t, model.calls, 1)
	require.Contains(t, model.calls[0][0].Content, "- 地区 (VARCHAR)")
	require.Contains(t, model.calls[0][0].Content, "- 销售额 (VARCHAR)")
	require.NotContains(t, model.calls[0][0].Content, "2026 年度销售统计")

	require.Len(t, turn.MergeResult, 2)
	require.Equal(t, types.MatchTypeDataAnalysis, turn.MergeResult[1].MatchType)
	require.Contains(t, turn.MergeResult[1].Content, `"地区":"华东"`)
	require.Contains(t, turn.MergeResult[1].Content, `"总额":"200"`)
}

func TestPluginDataAnalysis_SurfacesFailureNoticeToAnswerPrompt(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	dir := t.TempDir()
	localFiles := filesvc.NewLocalFileService(dir, "")
	savedPath, err := localFiles.SaveBytes(ctx, []byte("region,sales\neast,100\n"), 7, "sales.csv", false)
	require.NoError(t, err)

	knowledge := &types.Knowledge{
		ID: testKnowledgeID, KnowledgeBaseID: "kb", TenantID: 7,
		FileType: "csv", FileName: "sales.csv", FilePath: savedPath,
	}

	t.Run("SQL failure warns answer model without polluting citations or leaking UUIDs", func(t *testing.T) {
		model := &scriptedChat{replies: []string{
			`{"needs_analysis":true,"sql":"SELECT missing_col FROM dataset"}`,
			`{"needs_analysis":true,"sql":"SELECT missing_col FROM dataset"}`,
		}}
		plugin := NewPluginDataAnalysis(
			NewEventManager(), &stubModelService{model: model},
			&analysisStorageKBService{kb: &types.KnowledgeBase{ID: "kb", TenantID: 7}},
			&analysisStorageKnowledgeService{knowledge: knowledge},
			localFiles, nil, nil, db, nil,
		)
		turn := &types.ChatManage{}
		turn.SessionID = "fail-notice"
		turn.Query = "按地区统计销售额总和"
		turn.UserContent = "按地区统计销售额总和"
		turn.MergeResult = []*types.SearchResult{
			{
				ID: "chunk-text", KnowledgeID: knowledge.ID, KnowledgeFilename: knowledge.FileName,
				ChunkType: string(types.ChunkTypeText), Content: "east 100",
			},
			{
				ID: "chunk-summary", KnowledgeID: knowledge.ID, KnowledgeFilename: knowledge.FileName,
				ChunkType: string(types.ChunkTypeTableSummary), Content: "summary",
			},
		}
		pluginErr := plugin.OnEvent(ctx, types.DATA_ANALYSIS, turn, func() *PluginError { return nil })
		require.Nil(t, pluginErr)
		require.NotNil(t, turn.DataAnalysisFailure)
		require.Equal(t, "sales.csv", turn.DataAnalysisFailure.FileName)
		// Citations remain clean: only the real text chunk survives, no bogus failure SearchResult.
		require.Len(t, turn.MergeResult, 1)
		require.Equal(t, "chunk-text", turn.MergeResult[0].ID)

		messages, registry := prepareMessagesWithModelContext(ctx, turn)
		require.NotEmpty(t, messages)
		lastContent := messages[len(messages)-1].Content
		require.Contains(t, lastContent, "[Data Analysis Notice:")
		require.Contains(t, lastContent, "sales.csv")
		require.Contains(t, lastContent, "Do not manually calculate")
		require.Empty(t, registry.LeakedIdentifiers(messages))
	})

	t.Run("failure notice still reaches model when MergeResult has only table chunks", func(t *testing.T) {
		model := &scriptedChat{replies: []string{
			`{"needs_analysis":true,"sql":"SELECT missing_col FROM dataset"}`,
			`{"needs_analysis":true,"sql":"SELECT missing_col FROM dataset"}`,
		}}
		plugin := NewPluginDataAnalysis(
			NewEventManager(), &stubModelService{model: model},
			&analysisStorageKBService{kb: &types.KnowledgeBase{ID: "kb", TenantID: 7}},
			&analysisStorageKnowledgeService{knowledge: knowledge},
			localFiles, nil, nil, db, nil,
		)
		turn := &types.ChatManage{}
		turn.SessionID = "fail-empty-merge"
		turn.Query = "总销售额是多少"
		turn.UserContent = "总销售额是多少"
		turn.MergeResult = []*types.SearchResult{{
			ID: "chunk-summary", KnowledgeID: knowledge.ID, KnowledgeFilename: knowledge.FileName,
			ChunkType: string(types.ChunkTypeTableSummary), Content: "summary",
		}}
		require.Nil(t, plugin.OnEvent(ctx, types.DATA_ANALYSIS, turn, func() *PluginError { return nil }))
		require.Empty(t, turn.MergeResult)
		require.NotNil(t, turn.DataAnalysisFailure)

		messages, registry := prepareMessagesWithModelContext(ctx, turn)
		require.Contains(t, messages[len(messages)-1].Content, "[Data Analysis Notice:")
		require.Empty(t, registry.LeakedIdentifiers(messages))
	})

	t.Run("not-needed plan produces no failure notice", func(t *testing.T) {
		model := &scriptedChat{replies: []string{`{"needs_analysis":false,"sql":""}`}}
		plugin := NewPluginDataAnalysis(
			NewEventManager(), &stubModelService{model: model},
			&analysisStorageKBService{kb: &types.KnowledgeBase{ID: "kb", TenantID: 7}},
			&analysisStorageKnowledgeService{knowledge: knowledge},
			localFiles, nil, nil, db, nil,
		)
		turn := &types.ChatManage{}
		turn.SessionID = "not-needed"
		turn.Query = "这个文件包含哪些列"
		turn.UserContent = "这个文件包含哪些列"
		turn.MergeResult = []*types.SearchResult{{
			ID: "chunk-text", KnowledgeID: knowledge.ID, KnowledgeFilename: knowledge.FileName,
			ChunkType: string(types.ChunkTypeText), Content: "region,sales",
		}}
		require.Nil(t, plugin.OnEvent(ctx, types.DATA_ANALYSIS, turn, func() *PluginError { return nil }))
		require.Nil(t, turn.DataAnalysisFailure)

		messages, _ := prepareMessagesWithModelContext(ctx, turn)
		require.NotContains(t, messages[len(messages)-1].Content, "[Data Analysis Notice:")
	})
}
