package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fastschema/qjs"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// runJavaScriptDefaultTimeout bounds one script when the model does not
	// ask for a different window.
	runJavaScriptDefaultTimeout = 30 * time.Second
	// runJavaScriptMaxTimeout is the hard ceiling for one script. ExecutionTimeout
	// adds a small slack so the tool returns its own timeout result before the
	// engine cancels the call.
	runJavaScriptMaxTimeout = 120 * time.Second
	// runJavaScriptMaxCodeBytes caps the model-supplied source. The tool schema
	// advertises the same number to the model.
	runJavaScriptMaxCodeBytes = 24000
	// runJavaScriptMaxCalls bounds one script's tool fan-out for both auditing
	// and memory. A script that needs more belongs in several calls.
	runJavaScriptMaxCalls = 32
	// runJavaScriptMaxCallsHardCap is the most a caller may request. It keeps
	// one script from monopolizing the round even when max_calls is raised for
	// a large paginated export.
	runJavaScriptMaxCallsHardCap = 128
	// runJavaScriptNestedOutputChars is the per-call budget for a nested tool.
	// Nested output stays in the script, so it may be larger than the model's
	// context budget; the script's final answer is still truncated by the
	// registry to the normal model-facing limit.
	runJavaScriptNestedOutputChars = 256 * 1024
	// runJavaScriptMaxConsoleChars keeps console.log from competing with the
	// script's actual result.
	runJavaScriptMaxConsoleChars = 8 * 1024
	// runJavaScriptMaxReturnChars is a hard ceiling on one returned value before
	// it is parsed. The registry truncates the final envelope anyway.
	runJavaScriptMaxReturnChars = 1 << 20
	// runJavaScriptMaxOutputFiles bounds collected sandbox artifacts.
	runJavaScriptMaxOutputFiles = 20
	// runJavaScriptMaxInputBytes bounds one inputs entry. Inputs are copied from
	// prior tool results, so they bypass the model's token budget but must not
	// let one script pull an unbounded payload into WASM memory.
	runJavaScriptMaxInputBytes = 1 << 20
	// runJavaScriptMaxInputsTotalBytes bounds all inputs for one script.
	runJavaScriptMaxInputsTotalBytes = 8 << 20
	// runJavaScriptMaxLogLine caps one console line.
	runJavaScriptMaxLogLine = 2000
	// runJavaScriptMemoryLimit is the QuickJS heap limit inside the WASM
	// instance. The WASM linear memory itself is isolated from the Go heap.
	runJavaScriptMemoryLimit = 64 << 20
	// runJavaScriptMaxStackSize bounds the QuickJS call stack.
	runJavaScriptMaxStackSize = 1 << 20
)

var runJavaScriptSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "code": {
      "type": "string",
      "description": "JavaScript async function body. Call tools.<tool_name>(args); return a JSON-serializable value.",
      "maxLength": 24000
    },
    "timeout_ms": {
      "type": "integer",
      "minimum": 1000,
      "maximum": 120000,
      "default": 30000,
      "description": "Wall-clock limit for the script"
    },
    "max_calls": {
      "type": "integer",
      "minimum": 1,
      "maximum": 128,
      "default": 32,
      "description": "Maximum nested tool calls; raise for large paginated exports"
    },
    "inputs": {
      "type": "object",
      "description": "Data from earlier tool calls, keyed by name. Each value is an object with a call_id field.",
      "additionalProperties": {
        "type": "object",
        "properties": {
          "call_id": {"type": "string", "description": "A completed tool_call_id from this run"}
        },
        "required": ["call_id"],
        "additionalProperties": false
      }
    }
  },
  "required": ["code"],
  "additionalProperties": false
}`)

const runJavaScriptDescription = `Run JavaScript to compose the tools available in this conversation.

The script is an async function body. Call a tool with:
  const r = await tools.<tool_name>({...});

Each call returns {success, output, data, error, output_files, images}.
A failed tool returns success:false instead of throwing; check it. Use data when
present, otherwise parse output. Only your return value and console.log lines are
shown; intermediate output stays inside the script.

Data from an earlier tool call: pass it through inputs, for example
{"rows": {"call_id": "..."}}, and read inputs.rows. Do not paste the payload into
the script. When the sandbox file tools are available, sandbox.writeJSON(path,
value), sandbox.readJSON(path), sandbox.writeText, sandbox.readText and
sandbox.append provide the same access without escaping large strings by hand.

Use for mechanical multi-call work: two or more dependent calls whose sequence
you can write before seeing results, pagination/loops over a result set, or
filtering/deduplication/aggregation. It is also the preferred way to export data:
fetch rows inside the script, write JSON/CSV with sandbox.writeJSON or
tools.write_sandbox_file, and run conversion commands with tools.shell_exec.
Return only a compact summary and the output path, never raw rows or tool output.

Do not use for a single call; exploratory work where each result changes the next
action; independent calls that can be issued together; ask_user, confirmations or
secure forms; or image/attachment reads. Nested images are counted but not
returned.

Limits: max_calls defaults to 32 (up to 128), 30s default (120s max), 24KB
script, 256KB per nested result. Nested calls run sequentially.

Example:
  const out = [];
  for (const q of ["refund", "invoice"]) {
    const r = await tools.search_knowledge({query: q, max_results: 5});
    if (r.success) out.push(...(r.data?.results ?? []));
  }
  return out.slice(0, 8);`

// javascriptForbiddenSource blocks the routes out of the QuickJS realm that the
// qjs build leaves available: ES module import (which can reach the std/os
// modules) and the qjs: module namespace. Dynamic import is a keyword, so it
// cannot be assembled from strings once eval and the Function constructors are
// removed below.
var javascriptForbiddenSource = regexp.MustCompile(`(?i)\bimport\b|qjs:`)

// javascriptHardening removes the std/os modules and the eval/Function
// constructors that qjs exposes by default. Wazero already isolates network and
// filesystem access; this keeps a script from reaching the small surface qjs
// does expose (an empty temporary directory preopened at / and made read-only).
const javascriptHardening = `(() => {
  const g = globalThis;
  const F = Function;
  for (const k of ["std", "os", "bjson", "eval", "Function", "setTimeout", "setInterval",
                   "clearTimeout", "clearInterval", "print", "scriptArgs", "navigator"]) {
    try { delete g[k]; } catch (e) {}
  }
  const hide = (obj, key) => {
    try { Object.defineProperty(obj, key, {value: undefined}); } catch (e) {}
  };
  hide(Object, "constructor");
  hide(F.prototype, "constructor");
  hide(Object.getPrototypeOf(async function(){}), "constructor");
  hide(Object.getPrototypeOf(function*(){}), "constructor");
  hide(Object.getPrototypeOf(async function*(){}), "constructor");
})();`

// JavaScriptTool executes a model-written script inside a QuickJS WebAssembly
// runtime and exposes the run's registered tools through a narrow bridge. The
// script has no host filesystem, network or process access; every side effect
// goes through ToolRegistry, so existing validation, guards and policy apply.
type JavaScriptTool struct {
	BaseTool
	registry *ToolRegistry
}

// NewJavaScriptTool builds run_javascript over the registry as it exists when
// the call executes. The callable set is computed per call because MCP and
// other dynamic tools may be registered after this tool itself.
func NewJavaScriptTool(registry *ToolRegistry) *JavaScriptTool {
	return &JavaScriptTool{
		BaseTool: NewBaseTool(ToolRunJavaScript, runJavaScriptDescription, runJavaScriptSchema),
		registry: registry,
	}
}

// ExecutionTimeout gives the engine room for the largest script plus a small
// margin, so a script timeout is reported by the tool rather than by the
// engine's generic per-tool deadline.
func (t *JavaScriptTool) ExecutionTimeout() time.Duration {
	return runJavaScriptMaxTimeout + 5*time.Second
}

// Execute implements types.Tool.
func (t *JavaScriptTool) Execute(
	ctx context.Context, args json.RawMessage,
) (result *types.ToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf(ctx, "[Tool][JavaScript] panic: %v", recovered)
			result = &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("run_javascript failed unexpectedly: %v", recovered),
			}
			err = nil
		}
	}()

	var input struct {
		Code      string                `json:"code"`
		TimeoutMS int                   `json:"timeout_ms"`
		MaxCalls  int                   `json:"max_calls"`
		Inputs    map[string]jsInputRef `json:"inputs"`
	}
	if unpackErr := json.Unmarshal(args, &input); unpackErr != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", unpackErr),
		}, nil
	}
	code := strings.TrimSpace(input.Code)
	switch {
	case code == "":
		return &types.ToolResult{Success: false, Error: "code is required"}, nil
	case len(code) > runJavaScriptMaxCodeBytes:
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf("script is %d bytes; the limit is %d bytes. Return a shorter script.",
				len(code), runJavaScriptMaxCodeBytes),
		}, nil
	case javascriptForbiddenSource.MatchString(code):
		return &types.ToolResult{
			Success: false,
			Error:   "import and qjs: modules are not available inside run_javascript",
		}, nil
	}

	timeout := runJavaScriptDefaultTimeout
	if input.TimeoutMS > 0 {
		timeout = time.Duration(input.TimeoutMS) * time.Millisecond
	}
	if timeout > runJavaScriptMaxTimeout {
		timeout = runJavaScriptMaxTimeout
	}
	if timeout <= 0 {
		timeout = runJavaScriptDefaultTimeout
	}

	maxCalls := runJavaScriptMaxCalls
	if input.MaxCalls > 0 {
		maxCalls = min(input.MaxCalls, runJavaScriptMaxCallsHardCap)
	}
	resolvedInputs, inputErr := t.resolveInputs(ctx, input.Inputs)
	if inputErr != nil {
		return &types.ToolResult{Success: false, Error: inputErr.Error()}, nil
	}

	execution := &jsExecution{
		ctx:      ctx,
		timeout:  timeout,
		maxCalls: maxCalls,
		inputs:   resolvedInputs,
		registry: t.registry,
		callable: t.callableTools(),
	}
	return execution.run(code), nil
}

type jsInputRef struct {
	CallID string `json:"call_id"`
}

// resolveInputs copies prior tool results into JSON values for the script. It
// resolves only completed calls from the current run; no model payload has to
// be re-emitted, and no cross-session result is reachable.
func (t *JavaScriptTool) resolveInputs(
	ctx context.Context, refs map[string]jsInputRef,
) (map[string]json.RawMessage, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	lookup, ok := ToolResultLookupFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("inputs are not available for this run")
	}
	inputs := make(map[string]json.RawMessage, len(refs))
	total := 0
	for name, ref := range refs {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("inputs keys must not be empty")
		}
		callID := strings.TrimSpace(ref.CallID)
		if callID == "" {
			return nil, fmt.Errorf("inputs[%q] requires call_id", name)
		}
		raw, found := lookup(callID)
		if !found {
			return nil, fmt.Errorf("inputs[%q]: no completed tool call with id %q", name, callID)
		}
		if !json.Valid(raw) {
			return nil, fmt.Errorf("inputs[%q]: result is not valid JSON", name)
		}
		if len(raw) > runJavaScriptMaxInputBytes {
			return nil, fmt.Errorf("inputs[%q] is %d bytes; the limit is %d",
				name, len(raw), runJavaScriptMaxInputBytes)
		}
		total += len(raw)
		if total > runJavaScriptMaxInputsTotalBytes {
			return nil, fmt.Errorf("inputs total %d bytes; the limit is %d",
				total, runJavaScriptMaxInputsTotalBytes)
		}
		inputs[name] = raw
	}
	return inputs, nil
}

// callableTools is the model-facing set minus the tools that cannot work
// inside a script: run_javascript itself (recursion) and the interactive
// prompts, which need a client surface a script does not have.
func (t *JavaScriptTool) callableTools() map[string]types.Tool {
	if t.registry == nil {
		return nil
	}
	definitions := t.registry.GetModelFunctionDefinitions()
	callable := make(map[string]types.Tool, len(definitions))
	for _, definition := range definitions {
		if !scriptCallableTool(definition.Name) {
			continue
		}
		tool, err := t.registry.GetTool(definition.Name)
		if err != nil {
			continue
		}
		callable[definition.Name] = tool
	}
	return callable
}

func scriptCallableTool(name string) bool {
	switch name {
	case ToolRunJavaScript, ToolAskUser, ToolRequestSecureForm:
		return false
	default:
		return true
	}
}

type jsCallRecord struct {
	Tool  string `json:"tool"`
	OK    bool   `json:"ok"`
	MS    int64  `json:"ms"`
	Chars int    `json:"chars"`
	Error string `json:"error,omitempty"`
}

type jsExecution struct {
	ctx         context.Context
	scriptCtx   context.Context
	timeout     time.Duration
	maxCalls    int
	inputs      map[string]json.RawMessage
	registry    *ToolRegistry
	callable    map[string]types.Tool
	runtime     *qjs.Runtime
	quickjs     *qjs.Context
	global      *qjs.Value
	logs        []string
	logChars    int
	logCut      bool
	calls       []jsCallRecord
	callsMade   int
	outputFiles []string
	fileSeen    map[string]struct{}
	aborted     error
}

// run starts one isolated QuickJS WASM instance, installs the bridge, executes
// the wrapped script and renders the model-facing envelope.
func (e *jsExecution) run(code string) *types.ToolResult {
	started := time.Now()
	defer func() {
		failed := 0
		for _, call := range e.calls {
			if !call.OK {
				failed++
			}
		}
		logger.Infof(e.ctx, "[Tool][JavaScript] script_bytes=%d calls=%d failed=%d duration_ms=%d",
			len(code), len(e.calls), failed, time.Since(started).Milliseconds())
	}()

	// qjs mounts CWD into the WASM filesystem at /. A fresh empty directory
	// keeps that mount useless even if a script reaches the std/os modules.
	workDir, err := os.MkdirTemp("", "weknora-qjs-")
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to prepare script sandbox: %v", err)}
	}
	// qjs mounts this directory read-write at /. Making it read-only after
	// creation keeps an escaped std/os module from writing to the host, while
	// still giving the WASI preopen something to mount. Restore the mode
	// before cleanup so RemoveAll can unlink the tree.
	if chmodErr := os.Chmod(workDir, 0o500); chmodErr != nil {
		os.RemoveAll(workDir)
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to prepare script sandbox: %v", chmodErr)}
	}
	defer func() {
		_ = os.Chmod(workDir, 0o700)
		_ = os.RemoveAll(workDir)
	}()

	scriptCtx, cancel := context.WithTimeout(e.ctx, e.timeout)
	defer cancel()
	e.scriptCtx = scriptCtx

	// qjs v0.0.6 ignores MaxExecutionTime (the upstream C interrupt handler is
	// commented out), so the context deadline and CloseOnContextDone are what
	// actually stop a runaway script.
	runtime, err := qjs.New(qjs.Option{
		CWD:                workDir,
		Context:            scriptCtx,
		CloseOnContextDone: true,
		MemoryLimit:        runJavaScriptMemoryLimit,
		MaxStackSize:       runJavaScriptMaxStackSize,
		MaxExecutionTime:   int(e.timeout.Milliseconds()),
		Stdout:             io.Discard,
		Stderr:             io.Discard,
	})
	if err != nil {
		return e.failure(fmt.Sprintf("failed to start the JavaScript sandbox: %v", err))
	}
	e.runtime = runtime
	defer closeQJSRuntime(runtime)

	e.quickjs = runtime.Context()
	e.global = e.quickjs.Global()
	if err := e.installGlobals(); err != nil {
		return e.failure(err.Error())
	}

	wrapped := "const __qjs_stringify = JSON.stringify;\n" +
		"const __qjs_error = (err) => {" +
		" if (err === undefined || err === null) { return 'script failed'; }" +
		" try {" +
		"  const msg = (err.name ? String(err.name) + ': ' : '') +" +
		"   (err.message !== undefined ? String(err.message) : String(err));" +
		"  return err.stack ? msg + '\\n' + String(err.stack) : msg;" +
		" } catch (e) { return String(err); }" +
		"};\n" +
		"(async () => {\n" + code + "\n})().then(v => {" +
		" try { const s = __qjs_stringify(v === undefined ? null : v);" +
		" if (s === undefined) { throw new Error('return value is not JSON-serializable'); }" +
		" globalThis.__result_json = s;" +
		" } catch (err) { globalThis.__error = __qjs_error(err); }" +
		" }, err => { globalThis.__error = __qjs_error(err); });"

	evalErr, runtimeBroken := evalQJS(runtime, wrapped)
	scriptErr := e.scriptError(evalErr, runtimeBroken)
	if scriptErr != "" {
		return e.failure(scriptErr)
	}
	resultJSON := e.resultJSON()
	if len(resultJSON) > runJavaScriptMaxReturnChars {
		return e.failure(fmt.Sprintf(
			"return value is %d bytes; the limit is %d bytes. Return a summary.",
			len(resultJSON), runJavaScriptMaxReturnChars))
	}

	return &types.ToolResult{
		Success:     true,
		Output:      e.envelope(json.RawMessage(resultJSON), ""),
		Data:        e.summaryData(),
		OutputFiles: e.outputFiles,
	}
}

func closeQJSRuntime(runtime *qjs.Runtime) {
	if runtime == nil {
		return
	}
	// qjs panics when the runtime was closed by CloseOnContextDone. The script
	// is already over; dropping the instance is the only cleanup left.
	defer func() { _ = recover() }()
	runtime.Close()
}

// evalQJS turns qjs's panic-on-context-cancellation into an error. A panic
// leaves the WASM module closed, so callers must not touch runtime values
// afterwards.
func evalQJS(runtime *qjs.Runtime, code string) (err error, broken bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
			broken = true
		}
	}()
	_, err = runtime.Eval("run_javascript.js", qjs.Code(code))
	return err, false
}

func (e *jsExecution) installGlobals() error {
	if evalErr, _ := evalQJS(e.runtime, javascriptHardening); evalErr != nil {
		return fmt.Errorf("failed to prepare the JavaScript sandbox: %v", evalErr)
	}

	toolsObj := e.quickjs.NewObject()
	for name := range e.callable {
		name := name
		toolsObj.SetPropertyStr(name, e.quickjs.Function(func(this *qjs.This) (*qjs.Value, error) {
			return e.nativeToolCall(name, firstArgument(this))
		}))
	}
	toolsObj.SetPropertyStr("call", e.quickjs.Function(func(this *qjs.This) (*qjs.Value, error) {
		args := this.Args()
		if len(args) == 0 {
			return nil, fmt.Errorf("tools.call requires a tool name")
		}
		name := strings.TrimSpace(args[0].String())
		if name == "" {
			return nil, fmt.Errorf("tools.call requires a tool name")
		}
		var arg *qjs.Value
		if len(args) > 1 {
			arg = args[1]
		}
		return e.nativeToolCall(name, arg)
	}))
	e.global.SetPropertyStr("tools", toolsObj)

	inputsObj := e.quickjs.NewObject()
	for name, raw := range e.inputs {
		value := e.quickjs.ParseJSON(string(raw))
		if value == nil {
			return fmt.Errorf("inputs[%q] is not valid JSON", name)
		}
		inputsObj.SetPropertyStr(name, value)
	}
	e.global.SetPropertyStr("inputs", inputsObj)

	console := e.quickjs.NewObject()
	for _, level := range []string{"log", "info", "warn", "error"} {
		level := level
		console.SetPropertyStr(level, e.quickjs.Function(func(this *qjs.This) (*qjs.Value, error) {
			e.addLog(level, this.Args())
			return e.quickjs.NewUndefined(), nil
		}))
	}
	e.global.SetPropertyStr("console", console)

	if err := e.installSandboxHelpers(); err != nil {
		return err
	}

	e.global.SetPropertyStr("__result_json", e.quickjs.NewString("null"))
	e.global.SetPropertyStr("__error", e.quickjs.NewString(""))
	return nil
}

// installSandboxHelpers exposes a small file API backed by the existing
// sandbox file tools. JSON stringify/parse happen in JS so large object values
// never depend on Go-side value conversion. It is omitted when neither
// read_file nor write_sandbox_file is registered.
func (e *jsExecution) installSandboxHelpers() error {
	_, canRead := e.callable[ToolReadFile]
	_, canWrite := e.callable[ToolWriteSandboxFile]
	if !canRead && !canWrite {
		return nil
	}
	sandbox := e.quickjs.NewObject()
	if canWrite {
		sandbox.SetPropertyStr("writeText", e.quickjs.Function(func(this *qjs.This) (*qjs.Value, error) {
			args := this.Args()
			if len(args) < 2 {
				return nil, fmt.Errorf("sandbox.writeText(path, text) requires two arguments")
			}
			return e.writeSandboxFile(args[0].String(), args[1].String(), "")
		}))
		sandbox.SetPropertyStr("append", e.quickjs.Function(func(this *qjs.This) (*qjs.Value, error) {
			args := this.Args()
			if len(args) < 2 {
				return nil, fmt.Errorf("sandbox.append(path, text) requires two arguments")
			}
			return e.writeSandboxFile(args[0].String(), args[1].String(), "append")
		}))
	}
	if canRead {
		sandbox.SetPropertyStr("readText", e.quickjs.Function(func(this *qjs.This) (*qjs.Value, error) {
			args := this.Args()
			if len(args) == 0 {
				return nil, fmt.Errorf("sandbox.readText(path) requires a path")
			}
			result, err := e.readSandboxFile(args[0].String())
			if err != nil {
				return nil, err
			}
			return result.GetPropertyStr("output"), nil
		}))
	}
	e.global.SetPropertyStr("sandbox", sandbox)

	// JSON helpers stay in JS: qjs value conversion is lossy for large object
	// arguments, while JSON.stringify/parse already exist in the engine.
	var helpers []string
	if canWrite {
		helpers = append(helpers, "sandbox.writeJSON = (path, value) => sandbox.writeText(path, JSON.stringify(value));")
	}
	if canRead {
		helpers = append(helpers, "sandbox.readJSON = (path) => JSON.parse(sandbox.readText(path));")
	}
	if len(helpers) > 0 {
		if err, _ := evalQJS(e.runtime, strings.Join(helpers, "\n")); err != nil {
			return fmt.Errorf("failed to install sandbox helpers: %v", err)
		}
	}
	return nil
}

func (e *jsExecution) writeSandboxFile(path, content, mode string) (*qjs.Value, error) {
	payload := map[string]string{"path": path, "content": content}
	if mode != "" {
		payload["mode"] = mode
	}
	args, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return e.executeToolCall(ToolWriteSandboxFile, args)
}

func (e *jsExecution) readSandboxFile(path string) (*qjs.Value, error) {
	args, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		return nil, err
	}
	result, err := e.executeToolCall(ToolReadFile, args)
	if err != nil {
		return nil, err
	}
	success := result.GetPropertyStr("success")
	if success == nil || !success.Bool() {
		message := result.GetPropertyStr("error").String()
		if message == "" {
			message = "read_file failed"
		}
		return nil, fmt.Errorf("sandbox.read: %s", message)
	}
	return result, nil
}

func firstArgument(this *qjs.This) *qjs.Value {
	for _, arg := range this.Args() {
		return arg
	}
	return nil
}

func (e *jsExecution) nativeToolCall(name string, arg *qjs.Value) (*qjs.Value, error) {
	value, err := e.callTool(name, arg)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (e *jsExecution) addLog(level string, values []*qjs.Value) {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, jsValueString(value))
	}
	line := strings.Join(parts, " ")
	if level != "" && level != "log" && level != "info" {
		line = level + ": " + line
	}
	line = truncateScriptText(line, runJavaScriptMaxLogLine)
	if e.logCut {
		return
	}
	if e.logChars+len(line)+1 > runJavaScriptMaxConsoleChars {
		e.logCut = true
		e.logs = append(e.logs, "... (console output truncated)")
		return
	}
	e.logChars += len(line) + 1
	e.logs = append(e.logs, line)
}

func jsValueString(value *qjs.Value) string {
	if value == nil || value.IsUndefined() {
		return "undefined"
	}
	if value.IsNull() {
		return "null"
	}
	if value.IsObject() {
		if raw, err := value.JSONStringify(); err == nil {
			return raw
		}
	}
	return value.String()
}

// callTool converts a script-provided argument object and executes the tool.
func (e *jsExecution) callTool(name string, arg *qjs.Value) (*qjs.Value, error) {
	args, err := qjsArgumentJSON(arg)
	if err != nil {
		return nil, fmt.Errorf("arguments for %s: %v", name, err)
	}
	return e.executeToolCall(name, args)
}

// executeToolCall is the only bridge from the script to the rest of the agent.
// A tool failure is a normal return value so the script can branch on it; a
// missing or interactive tool throws, because retrying cannot fix it.
func (e *jsExecution) executeToolCall(name string, args json.RawMessage) (*qjs.Value, error) {
	if e.aborted != nil {
		return nil, e.aborted
	}
	if !scriptCallableTool(name) {
		return nil, fmt.Errorf("tool %q cannot be called from run_javascript; call it directly", name)
	}
	if _, ok := e.callable[name]; !ok {
		return nil, fmt.Errorf("tool %q is not available in this conversation", name)
	}
	if e.callsMade >= e.maxCalls {
		return nil, fmt.Errorf("script exceeded the %d tool-call limit", e.maxCalls)
	}
	e.callsMade++
	index := e.callsMade
	meta, _ := ToolExecFromContext(e.scriptCtx)
	e.emitNestedProgress(meta, index, name, "start", false, 0, 0, "")
	if meta != nil && meta.DecodeNestedArgs != nil {
		args = meta.DecodeNestedArgs(name, args)
	}

	started := time.Now()
	result, execErr := e.registry.ExecuteTool(e.nestedContext(), name, args)
	duration := time.Since(started).Milliseconds()

	record := jsCallRecord{Tool: name, MS: duration}
	if result != nil {
		record.OK = result.Success
		record.Chars = len(result.Output)
		if result.Error != "" {
			record.Error = truncateScriptText(result.Error, 300)
		}
		e.collectOutputFiles(result.OutputFiles)
	} else if execErr != nil {
		record.Error = truncateScriptText(execErr.Error(), 300)
	}
	e.calls = append(e.calls, record)

	success := result != nil && result.Success
	chars := 0
	errMsg := ""
	if result != nil {
		chars = len(result.Output)
		errMsg = result.Error
	}
	if execErr != nil && errMsg == "" {
		errMsg = execErr.Error()
	}
	e.emitNestedProgress(meta, index, name, "result", success, duration, chars, errMsg)

	if result != nil && result.Suspend != nil {
		e.aborted = fmt.Errorf(
			"tool %q needs user input, which run_javascript cannot show; call it directly", name)
		return nil, e.aborted
	}
	if execErr != nil {
		return nil, fmt.Errorf("tool %s failed: %v", name, execErr)
	}
	if result == nil {
		return nil, fmt.Errorf("tool %s returned no result", name)
	}

	obj := e.quickjs.NewObject()
	obj.SetPropertyStr("success", e.quickjs.NewBool(result.Success))
	obj.SetPropertyStr("output", e.quickjs.NewString(result.Output))
	if result.Error != "" {
		obj.SetPropertyStr("error", e.quickjs.NewString(result.Error))
	}
	if result.Data != nil {
		if raw, marshalErr := json.Marshal(result.Data); marshalErr == nil {
			obj.SetPropertyStr("data", e.quickjs.ParseJSON(string(raw)))
		}
	}
	if len(result.OutputFiles) > 0 {
		if raw, marshalErr := json.Marshal(result.OutputFiles); marshalErr == nil {
			obj.SetPropertyStr("output_files", e.quickjs.ParseJSON(string(raw)))
		}
	}
	if len(result.Images) > 0 {
		obj.SetPropertyStr("images", e.quickjs.NewInt32(int32(len(result.Images))))
	}
	return obj, nil
}

func qjsArgumentJSON(arg *qjs.Value) (json.RawMessage, error) {
	if arg == nil || arg.IsUndefined() || arg.IsNull() {
		return json.RawMessage("{}"), nil
	}
	raw, err := arg.JSONStringify()
	if err != nil {
		return nil, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "undefined" {
		return json.RawMessage("{}"), nil
	}
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("arguments are not valid JSON")
	}
	return json.RawMessage(raw), nil
}

func (e *jsExecution) emitNestedProgress(
	meta *ToolExecContext, index int, name, phase string,
	success bool, duration int64, chars int, errMsg string,
) {
	if meta == nil || meta.EventBus == nil || meta.ToolCallID == "" {
		return
	}
	nestedID := fmt.Sprintf("%s-js-%d", meta.ToolCallID, index)
	_ = meta.EventBus.Emit(e.scriptCtx, event.Event{
		ID:        nestedID + "-" + phase,
		Type:      event.EventAgentJavaScriptProgress,
		SessionID: meta.SessionID,
		Data: event.JavaScriptProgressData{
			ToolCallID:   meta.ToolCallID,
			NestedCallID: nestedID,
			ToolName:     name,
			Phase:        phase,
			Index:        index,
			Success:      success,
			DurationMS:   duration,
			Chars:        chars,
			Error:        errMsg,
		},
	})
}

func (e *jsExecution) nestedContext() context.Context {
	meta := &ToolExecContext{}
	if existing, ok := ToolExecFromContext(e.scriptCtx); ok && existing != nil {
		copied := *existing
		meta = &copied
	}
	meta.Scripted = true
	meta.Mode = types.RunModeUnattended
	meta.Snapshot = nil
	meta.Answered = nil
	meta.ApprovalCtx = e.scriptCtx
	if deadline, ok := e.scriptCtx.Deadline(); ok {
		meta.ExecTimeout = time.Until(deadline)
	}
	ctx := WithToolExecContext(e.scriptCtx, meta)
	return withNestedToolBudget(ctx, runJavaScriptNestedOutputChars)
}

func (e *jsExecution) collectOutputFiles(files []string) {
	if len(files) == 0 {
		return
	}
	if e.fileSeen == nil {
		e.fileSeen = make(map[string]struct{}, len(files))
	}
	for _, file := range files {
		if file == "" {
			continue
		}
		if _, ok := e.fileSeen[file]; ok {
			continue
		}
		if len(e.outputFiles) >= runJavaScriptMaxOutputFiles {
			return
		}
		e.fileSeen[file] = struct{}{}
		e.outputFiles = append(e.outputFiles, file)
	}
}

func (e *jsExecution) scriptError(evalErr error, runtimeBroken bool) string {
	if e.aborted != nil {
		return e.aborted.Error()
	}
	// Check the context before reading globals: qjs closes the WASM module on
	// cancellation, and any value access after that panics.
	if e.scriptCtx != nil && e.scriptCtx.Err() != nil {
		return fmt.Sprintf("script stopped after %s: %v", e.timeout, e.scriptCtx.Err())
	}
	if !runtimeBroken {
		if msg := strings.TrimSpace(e.globalString("__error")); msg != "" {
			return truncateScriptText(msg, 4000)
		}
	}
	if evalErr != nil {
		return truncateScriptText(evalErr.Error(), 4000)
	}
	return ""
}

func (e *jsExecution) resultJSON() string {
	raw := strings.TrimSpace(e.globalString("__result_json"))
	if raw == "" || raw == "undefined" || !json.Valid([]byte(raw)) {
		return "null"
	}
	return raw
}

func (e *jsExecution) globalString(name string) (text string) {
	if e.global == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			text = ""
		}
	}()
	value := e.global.GetPropertyStr(name)
	if value == nil || value.IsUndefined() || value.IsNull() {
		return ""
	}
	return value.String()
}

func (e *jsExecution) failure(message string) *types.ToolResult {
	return &types.ToolResult{
		Success:     false,
		Output:      e.envelope(nil, message),
		Error:       message,
		OutputFiles: e.outputFiles,
	}
}

func (e *jsExecution) envelope(result json.RawMessage, scriptErr string) string {
	envelope := map[string]interface{}{"calls": e.calls}
	if len(e.logs) > 0 {
		envelope["logs"] = e.logs
	}
	if scriptErr != "" {
		envelope["error"] = scriptErr
	} else if result != nil {
		envelope["result"] = result
	} else {
		envelope["result"] = nil
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Sprintf(`{"calls":%d,"error":%q}`, len(e.calls), scriptErr)
	}
	return string(raw)
}

func (e *jsExecution) summaryData() map[string]interface{} {
	failed := 0
	for _, call := range e.calls {
		if !call.OK {
			failed++
		}
	}
	return map[string]interface{}{
		"calls":        len(e.calls),
		"failed_calls": failed,
	}
}

func truncateScriptText(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}
