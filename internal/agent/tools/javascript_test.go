package tools

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

type scriptFakeTool struct {
	BaseTool
	fn func(context.Context, json.RawMessage) (*types.ToolResult, error)
}

func (f *scriptFakeTool) Execute(
	ctx context.Context, args json.RawMessage,
) (*types.ToolResult, error) {
	return f.fn(ctx, args)
}

func newScriptFakeTool(
	name string, fn func(context.Context, json.RawMessage) (*types.ToolResult, error),
) *scriptFakeTool {
	return &scriptFakeTool{
		BaseTool: NewBaseTool(name, "fake "+name, json.RawMessage(`{"type":"object"}`)),
		fn:       fn,
	}
}

func newScriptTestRegistry() *ToolRegistry {
	registry := NewToolRegistry()
	registry.RegisterTool(newScriptFakeTool(ToolSearchKnowledge, func(
		_ context.Context, raw json.RawMessage,
	) (*types.ToolResult, error) {
		var input struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(raw, &input)
		return &types.ToolResult{
			Success: true,
			Output:  "RAW_SEARCH_OUTPUT",
			Data: map[string]interface{}{
				"results": []map[string]interface{}{
					{"chunk_id": "chunk-1", "title": "Result:" + input.Query},
				},
			},
		}, nil
	}))
	registry.RegisterTool(newScriptFakeTool(ToolAskUser, func(
		context.Context, json.RawMessage,
	) (*types.ToolResult, error) {
		return &types.ToolResult{Success: true, Output: "should not run"}, nil
	}))
	return registry
}

func TestJavaScriptComposesToolsWithoutLeakingNestedOutput(t *testing.T) {
	tool := NewJavaScriptTool(newScriptTestRegistry())
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "const a = await tools.search_knowledge({query:'x'}); const b = await tools.search_knowledge({query:'y'}); return [a.data.results[0].title, b.data.results[0].title];"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, `"result":["Result:x","Result:y"]`)
	require.NotContains(t, result.Output, "RAW_SEARCH_OUTPUT")
	require.Contains(t, result.Output, `"calls":[{"tool":"search_knowledge","ok":true`)
}

func TestJavaScriptReturnsToolFailuresAsValues(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(newScriptFakeTool("flaky", func(
		context.Context, json.RawMessage,
	) (*types.ToolResult, error) {
		return &types.ToolResult{Success: false, Error: "boom", Output: "partial"}, nil
	}))
	tool := NewJavaScriptTool(registry)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "const r = await tools.flaky({}); return {ok: r.success, error: r.error, output: r.output};"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, `"ok":false`)
	require.Contains(t, result.Output, `"error":"boom"`)
	require.Contains(t, result.Output, `"output":"partial"`)
}

func TestJavaScriptHidesRecursiveAndPromptTools(t *testing.T) {
	tool := NewJavaScriptTool(newScriptTestRegistry())
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "return [typeof tools.run_javascript, typeof tools.ask_user, typeof tools.call];"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, `"result":["undefined","undefined","function"]`)

	result, err = tool.Execute(context.Background(), json.RawMessage(`{
		"code": "return tools.call('ask_user', {});"
	}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "cannot be called from run_javascript")
}

func TestJavaScriptEnforcesCallLimit(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(newScriptFakeTool("echo", func(
		context.Context, json.RawMessage,
	) (*types.ToolResult, error) {
		return &types.ToolResult{Success: true, Output: "ok"}, nil
	}))
	tool := NewJavaScriptTool(registry)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "for (let i = 0; i < 40; i++) { await tools.echo({n: i}); } return 'done';"
	}`))

	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "tool-call limit")
}

func TestJavaScriptInterruptsInfiniteLoop(t *testing.T) {
	tool := NewJavaScriptTool(newScriptTestRegistry())

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "while (true) {}", "timeout_ms": 1000
	}`))

	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "script stopped after")
}

func TestJavaScriptRefusesSuspendingTool(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(newScriptFakeTool("needs_answer", func(
		context.Context, json.RawMessage,
	) (*types.ToolResult, error) {
		return &types.ToolResult{
			Success: false,
			Error:   "waiting for the user",
			Suspend: &types.PromptSuspension{PromptID: "p1", Kind: types.AgentPromptKindAskUser},
		}, nil
	}))
	tool := NewJavaScriptTool(registry)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "return await tools.needs_answer({});"
	}`))

	require.NoError(t, err)
	require.False(t, result.Success)
	require.Nil(t, result.Suspend)
	require.Contains(t, result.Error, "cannot show")
}

func TestJavaScriptCollectsOutputFilesAndLogs(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(newScriptFakeTool("write_artifact", func(
		context.Context, json.RawMessage,
	) (*types.ToolResult, error) {
		return &types.ToolResult{
			Success:     true,
			Output:      "written",
			OutputFiles: []string{"sandbox:a.txt", "sandbox:b.txt"},
		}, nil
	}))
	tool := NewJavaScriptTool(registry)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "console.log('start', {step: 1}); await tools.write_artifact({}); await tools.write_artifact({}); console.warn('done'); return 'ok';"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.ElementsMatch(t, []string{"sandbox:a.txt", "sandbox:b.txt"}, result.OutputFiles)
	require.Contains(t, result.Output, `"logs":["start {\"step\":1}","warn: done"]`)
}

func TestJavaScriptRejectsNonSerializableReturn(t *testing.T) {
	tool := NewJavaScriptTool(newScriptTestRegistry())

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "return () => 1;"
	}`))

	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "JSON-serializable")
}

func TestJavaScriptNestedCallsAreMarkedScripted(t *testing.T) {
	registry := NewToolRegistry()
	var scripted bool
	var mode types.RunMode
	registry.RegisterTool(newScriptFakeTool("inspect_context", func(
		ctx context.Context, _ json.RawMessage,
	) (*types.ToolResult, error) {
		meta, _ := ToolExecFromContext(ctx)
		scripted = meta != nil && meta.Scripted
		if meta != nil {
			mode = meta.Mode
		}
		return &types.ToolResult{Success: true, Output: "ok"}, nil
	}))
	tool := NewJavaScriptTool(registry)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "return await tools.inspect_context({});"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.True(t, scripted)
	require.Equal(t, types.RunModeUnattended, mode)
}

func TestJavaScriptRemovesSandboxEscapeGlobals(t *testing.T) {
	tool := NewJavaScriptTool(newScriptTestRegistry())
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "return [typeof std, typeof os, typeof eval, typeof Function, typeof ({}).constructor.constructor, typeof (async function(){}).constructor, typeof (function*(){}).constructor].join(',');"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output,
		`"result":"undefined,undefined,undefined,undefined,undefined,undefined,undefined"`)
}

func TestJavaScriptRejectsModuleImport(t *testing.T) {
	tool := NewJavaScriptTool(newScriptTestRegistry())
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "return import('qjs:std');"
	}`))

	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "import and qjs")
}

func TestJavaScriptThroughRegistry(t *testing.T) {
	registry := newScriptTestRegistry()
	registry.RegisterTool(NewJavaScriptTool(registry))

	result, err := registry.ExecuteTool(context.Background(), ToolRunJavaScript, json.RawMessage(`{
		"code": "return {title: (await tools.search_knowledge({query:'x'})).data.results[0].title, canRecurse: typeof tools.run_javascript};"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, `"title":"Result:x"`)
	require.Contains(t, result.Output, `"canRecurse":"undefined"`)
}

func TestJavaScriptHonorsCallerMaxCalls(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(newScriptFakeTool("echo", func(
		context.Context, json.RawMessage,
	) (*types.ToolResult, error) {
		return &types.ToolResult{Success: true, Output: "ok"}, nil
	}))
	tool := NewJavaScriptTool(registry)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "for (let i = 0; i < 3; i++) { await tools.echo({n: i}); } return 'done';",
		"max_calls": 2
	}`))

	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "2 tool-call limit")
}

func TestJavaScriptSandboxJSONHelpers(t *testing.T) {
	registry := NewToolRegistry()
	files := map[string]string{}
	registry.RegisterTool(newScriptFakeTool(ToolWriteSandboxFile, func(
		_ context.Context, raw json.RawMessage,
	) (*types.ToolResult, error) {
		var input struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Mode    string `json:"mode"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return &types.ToolResult{Success: false, Error: err.Error()}, nil
		}
		if input.Mode == "append" {
			files[input.Path] += input.Content
		} else {
			files[input.Path] = input.Content
		}
		return &types.ToolResult{Success: true, Output: "written"}, nil
	}))
	registry.RegisterTool(newScriptFakeTool(ToolReadFile, func(
		_ context.Context, raw json.RawMessage,
	) (*types.ToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(raw, &input)
		content, ok := files[input.Path]
		if !ok {
			return &types.ToolResult{Success: false, Error: "not found"}, nil
		}
		return &types.ToolResult{Success: true, Output: content}, nil
	}))
	tool := NewJavaScriptTool(registry)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "await sandbox.writeJSON('/workspace/data.json', {a: 7}); const v = await sandbox.readJSON('/workspace/data.json'); return v.a;"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error+"\n"+result.Output)
	require.Contains(t, result.Output, `"result":7`)
	require.Equal(t, `{"a":7}`, files["/workspace/data.json"])
}

func TestJavaScriptSandboxHelpersRequireFileTools(t *testing.T) {
	tool := NewJavaScriptTool(newScriptTestRegistry())
	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"code": "return typeof sandbox;"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, `"result":"undefined"`)
}

func TestJavaScriptInputsResolveEarlierToolResults(t *testing.T) {
	tool := NewJavaScriptTool(newScriptTestRegistry())
	ctx := WithToolResultLookup(context.Background(), func(callID string) (json.RawMessage, bool) {
		if callID != "call_1" {
			return nil, false
		}
		return json.RawMessage(`{"rows":[{"id":1},{"id":2}]}`), true
	})

	result, err := tool.Execute(ctx, json.RawMessage(`{
		"code": "return inputs.docs.rows.length;",
		"inputs": {"docs": {"call_id": "call_1"}}
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, `"result":2`)
}

func TestJavaScriptInputsRejectUnknownCall(t *testing.T) {
	tool := NewJavaScriptTool(newScriptTestRegistry())
	ctx := WithToolResultLookup(context.Background(), func(string) (json.RawMessage, bool) {
		return nil, false
	})

	result, err := tool.Execute(ctx, json.RawMessage(`{
		"code": "return inputs.docs;",
		"inputs": {"docs": {"call_id": "missing"}}
	}`))

	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "no completed tool call")
}

func TestToolResultInputValuePrefersDataThenJSONOutput(t *testing.T) {
	require.JSONEq(t, `{"a":1}`, string(ToolResultInputValue(&types.ToolResult{
		Success: true,
		Data:    map[string]interface{}{"a": 1},
		Output:  "ignored",
	})))
	require.JSONEq(t, `{"b":2}`, string(ToolResultInputValue(&types.ToolResult{
		Success: true,
		Output:  `{"b":2}`,
	})))
	require.JSONEq(t, `"plain text"`, string(ToolResultInputValue(&types.ToolResult{
		Success: true,
		Output:  "plain text",
	})))
}

func TestJavaScriptDecodesNestedArguments(t *testing.T) {
	registry := NewToolRegistry()
	var received string
	registry.RegisterTool(newScriptFakeTool("echo", func(
		_ context.Context, raw json.RawMessage,
	) (*types.ToolResult, error) {
		received = string(raw)
		return &types.ToolResult{Success: true, Output: "ok"}, nil
	}))
	tool := NewJavaScriptTool(registry)
	ctx := WithToolExecContext(context.Background(), &ToolExecContext{
		Mode: types.RunModeUnattended,
		DecodeNestedArgs: func(string, json.RawMessage) json.RawMessage {
			return json.RawMessage(`{"decoded":true}`)
		},
	})

	result, err := tool.Execute(ctx, json.RawMessage(`{
		"code": "return await tools.echo({handle:'doc://1'});"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.JSONEq(t, `{"decoded":true}`, received)
}

func TestToolResultInputValueUnwrapsPriorJavaScriptResult(t *testing.T) {
	raw := ToolResultInputValueForTool(ToolRunJavaScript, &types.ToolResult{
		Success: true,
		Output:  `{"result":{"rows":[1,2]},"calls":[],"logs":[]}`,
		Data:    map[string]interface{}{"calls": 0, "failed_calls": 0},
	})
	require.JSONEq(t, `{"rows":[1,2]}`, string(raw))
}

func TestJavaScriptEmitsNestedProgressEvents(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool(newScriptFakeTool("echo", func(
		context.Context, json.RawMessage,
	) (*types.ToolResult, error) {
		return &types.ToolResult{Success: true, Output: "ok"}, nil
	}))
	bus := event.NewEventBus()
	var mu sync.Mutex
	var progress []event.JavaScriptProgressData
	bus.On(event.EventAgentJavaScriptProgress, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.JavaScriptProgressData)
		if ok {
			mu.Lock()
			progress = append(progress, data)
			mu.Unlock()
		}
		return nil
	})
	ctx := WithToolExecContext(context.Background(), &ToolExecContext{
		SessionID:  "session",
		ToolCallID: "outer",
		EventBus:   bus,
		Mode:       types.RunModeUnattended,
	})

	tool := NewJavaScriptTool(registry)
	result, err := tool.Execute(ctx, json.RawMessage(`{
		"code": "await tools.echo({n: 1}); await tools.echo({n: 2}); return 'done';"
	}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, progress, 4)
	require.Equal(t, "start", progress[0].Phase)
	require.Equal(t, "result", progress[1].Phase)
	require.Equal(t, "outer-js-1", progress[0].NestedCallID)
	require.Equal(t, "outer-js-2", progress[2].NestedCallID)
	require.True(t, progress[3].Success)
	require.Equal(t, "echo", progress[3].ToolName)
}
