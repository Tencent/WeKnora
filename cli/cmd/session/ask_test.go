package sessioncmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// scriptedAskSvc serves a canned stream of agent events to runAsk.
type scriptedAskSvc struct {
	createResp *sdk.Session
	createErr  error
	events     []*sdk.AgentStreamResponse
	streamErr  error
	got        struct {
		sessionID string
		req       *sdk.AgentQARequest
	}
}

func (s *scriptedAskSvc) CreateSession(_ context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error) {
	if s.createResp == nil && s.createErr == nil {
		return &sdk.Session{ID: "sess_auto", Title: req.Title}, nil
	}
	return s.createResp, s.createErr
}

func (s *scriptedAskSvc) AgentQAStreamWithRequest(_ context.Context, sessionID string, req *sdk.AgentQARequest, cb sdk.AgentEventCallback) error {
	s.got.sessionID = sessionID
	s.got.req = req
	for _, e := range s.events {
		if err := cb(e); err != nil {
			return err
		}
	}
	return s.streamErr
}

func answerEvent(content string) *sdk.AgentStreamResponse {
	return &sdk.AgentStreamResponse{ResponseType: sdk.AgentResponseTypeAnswer, Content: content}
}
func doneEvent() *sdk.AgentStreamResponse {
	return &sdk.AgentStreamResponse{ResponseType: sdk.AgentResponseTypeAnswer, Done: true}
}
func toolCallEvent(id, name string) *sdk.AgentStreamResponse {
	return &sdk.AgentStreamResponse{
		ResponseType: sdk.AgentResponseTypeToolCall,
		ID:           id,
		Content:      name,
	}
}
func referencesEvent(refs []*sdk.SearchResult) *sdk.AgentStreamResponse {
	return &sdk.AgentStreamResponse{
		ResponseType:        sdk.AgentResponseTypeReferences,
		KnowledgeReferences: refs,
	}
}

// textOpts returns a FormatOptions configured for the text render path —
// the most common shape under test.
func textOpts() *cmdutil.FormatOptions {
	return &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
}

// ndjsonOpts returns a FormatOptions for the NDJSON event-stream path.
// --format json routes here too for streaming commands.
func ndjsonOpts() *cmdutil.FormatOptions {
	return &cmdutil.FormatOptions{Mode: cmdutil.FormatNDJSON}
}

// jsonOpts returns a FormatOptions configured for the JSON path.
// In v0.7 --format json routes to the NDJSON stream path for streaming commands.
func jsonOpts() *cmdutil.FormatOptions {
	return &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
}

// TestSessionAsk_NDJSON_FirstLineIsInit verifies that the NDJSON path (--format json)
// always injects an "init" line first, carrying session_id and agent_id.
func TestSessionAsk_NDJSON_FirstLineIsInit(t *testing.T) {
	out, errBuf := iostreams.SetForTest(t)
	svc := &scriptedAskSvc{
		events: []*sdk.AgentStreamResponse{
			answerEvent("answer"),
			doneEvent(),
		},
	}
	opts := &AskOptions{AgentID: "ag_x", Query: "ping"}
	if err := runAsk(context.Background(), opts, jsonOpts(), svc); err != nil {
		t.Fatalf("runAsk: %v", err)
	}

	// NDJSON mode must NOT print the session hint to stderr.
	if errBuf.Len() != 0 {
		t.Errorf("expected empty stderr in NDJSON mode, got %q", errBuf.String())
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("no output")
	}
	var first struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first line not JSON: %v\n  %s", err, lines[0])
	}
	if first.Type != "init" {
		t.Errorf("first line type: got %q, want init", first.Type)
	}
	if first.SessionID != "sess_auto" {
		t.Errorf("init.session_id: got %q, want sess_auto", first.SessionID)
	}
	if first.AgentID != "ag_x" {
		t.Errorf("init.agent_id: got %q, want ag_x", first.AgentID)
	}
}

// TestSessionAsk_NDJSON_PassthroughEvents verifies init + N SDK events = N+1 total lines.
func TestSessionAsk_NDJSON_PassthroughEvents(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &scriptedAskSvc{
		events: []*sdk.AgentStreamResponse{
			answerEvent("hello"),
			doneEvent(),
		},
	}
	opts := &AskOptions{AgentID: "ag_x", Query: "hi"}
	if err := runAsk(context.Background(), opts, jsonOpts(), svc); err != nil {
		t.Fatalf("runAsk: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	// 1 init + 2 SDK events = 3 lines.
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), out.String())
	}
	// Each must be valid JSON.
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d not valid JSON: %v\n  %s", i+1, err, line)
		}
	}
}

// TestSessionAsk_NDJSON_JSONEqualsNDJSON verifies that --format json and --format ndjson
// produce identical NDJSON streams for session ask.
func TestSessionAsk_NDJSON_JSONEqualsNDJSON(t *testing.T) {
	events := []*sdk.AgentStreamResponse{
		answerEvent("hello"),
		doneEvent(),
	}

	runWith := func(mode cmdutil.FormatMode) string {
		out, _ := iostreams.SetForTest(t)
		svc := &scriptedAskSvc{events: events}
		opts := &AskOptions{AgentID: "ag_x", Query: "q"}
		fopts := &cmdutil.FormatOptions{Mode: mode}
		if err := runAsk(context.Background(), opts, fopts, svc); err != nil {
			t.Fatalf("runAsk(%s): %v", mode, err)
		}
		return out.String()
	}

	jsonOut := runWith(cmdutil.FormatJSON)
	ndjsonOut := runWith(cmdutil.FormatNDJSON)
	if jsonOut != ndjsonOut {
		t.Errorf("--format json and --format ndjson differ:\n  json:   %q\n  ndjson: %q", jsonOut, ndjsonOut)
	}
}

// TestSessionAsk_AutoCreatedSessionID_PassedAsAgentRequest checks the session id
// flows from auto-create through to the SDK stream call.
func TestSessionAsk_AutoCreatedSessionID_PassedAsAgentRequest(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedAskSvc{events: []*sdk.AgentStreamResponse{doneEvent()}}
	opts := &AskOptions{AgentID: "ag_42", Query: "x"}
	if err := runAsk(context.Background(), opts, ndjsonOpts(), svc); err != nil {
		t.Fatalf("runAsk: %v", err)
	}
	if svc.got.sessionID != "sess_auto" {
		t.Errorf("agent-chat got sessionID=%q, want sess_auto", svc.got.sessionID)
	}
	if svc.got.req == nil || svc.got.req.AgentID != "ag_42" {
		t.Errorf("AgentID not forwarded: %+v", svc.got.req)
	}
	if !svc.got.req.AgentEnabled {
		t.Error("AgentEnabled must be true for session ask")
	}
}

func TestSessionAsk_ExistingSessionID_SkipsCreate(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	created := false
	svc := &scriptedAskSvc{events: []*sdk.AgentStreamResponse{doneEvent()}}
	// Wrap CreateSession to detect call.
	svc.createResp = &sdk.Session{ID: "should_not_be_used"}
	wrapped := &createSessionTracker{AskService: svc, called: &created}
	opts := &AskOptions{AgentID: "ag", Query: "x", SessionID: "sess_existing"}
	if err := runAsk(context.Background(), opts, ndjsonOpts(), wrapped); err != nil {
		t.Fatalf("runAsk: %v", err)
	}
	if created {
		t.Error("CreateSession should not be called when --session is set")
	}
	if svc.got.sessionID != "sess_existing" {
		t.Errorf("agent-chat got sessionID=%q, want sess_existing", svc.got.sessionID)
	}
}

type createSessionTracker struct {
	AskService
	called *bool
}

func (c *createSessionTracker) CreateSession(ctx context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error) {
	*c.called = true
	return c.AskService.CreateSession(ctx, req)
}

// TestSessionAsk_EmptyQuery_Rejected checks validation fires before any SDK call.
func TestSessionAsk_EmptyQuery_Rejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedAskSvc{}
	opts := &AskOptions{AgentID: "ag", Query: ""}
	err := runAsk(context.Background(), opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected input.invalid_argument, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("expected input.invalid_argument, got %v", err)
	}
}

// TestSessionAsk_StreamAbortBeforeDone_MapsToSSEStreamAborted uses the human path
// because the NDJSON path does not buffer/validate Done events.
func TestSessionAsk_StreamAbortBeforeDone_MapsToSSEStreamAborted(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedAskSvc{
		events: []*sdk.AgentStreamResponse{
			answerEvent("partial"),
		},
		streamErr: errors.New("connection reset"),
	}
	opts := &AskOptions{AgentID: "ag", Query: "x"}
	// Text path (textOpts) validates Done; NDJSON path does not buffer.
	err := runAsk(context.Background(), opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected stream-aborted error")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeSSEStreamAborted {
		t.Errorf("expected local.sse_stream_aborted, got %v", err)
	}
}

// TestSessionAsk_NoDoneEvent_MapsToSSEStreamAborted uses the human path
// because the NDJSON path does not validate Done events.
func TestSessionAsk_NoDoneEvent_MapsToSSEStreamAborted(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedAskSvc{events: []*sdk.AgentStreamResponse{answerEvent("incomplete")}}
	opts := &AskOptions{AgentID: "ag", Query: "x"}
	err := runAsk(context.Background(), opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected stream-aborted error")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeSSEStreamAborted {
		t.Errorf("expected local.sse_stream_aborted, got %v", err)
	}
}

func TestSessionAsk_CreateSessionFails_MapsToSessionCreateFailed(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedAskSvc{createErr: errors.New("connection refused")}
	opts := &AskOptions{AgentID: "ag", Query: "x"}
	err := runAsk(context.Background(), opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected session_create_failed")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeSessionCreateFailed {
		t.Errorf("expected server.session_create_failed, got %v", err)
	}
}

func TestSessionAsk_Cancellation_MapsToOperationCancelled(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	svc := &scriptedAskSvc{streamErr: context.Canceled}
	opts := &AskOptions{AgentID: "ag", Query: "x"}
	// NDJSON path also handles cancellation correctly.
	err := runAsk(ctx, opts, ndjsonOpts(), svc)
	if err == nil {
		t.Fatal("expected operation.cancelled")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeOperationCancelled {
		t.Errorf("expected operation.cancelled, got %v", err)
	}
}

// Sanity: text-mode output writes the answer body and a tool-trace footer.
func TestSessionAsk_Text_Accumulate_PrintsAnswerAndFooter(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &scriptedAskSvc{events: []*sdk.AgentStreamResponse{
		answerEvent("hello"),
		toolCallEvent("c1", "knowledge_search"),
		doneEvent(),
	}}
	opts := &AskOptions{AgentID: "ag", Query: "x"}
	if err := runAsk(context.Background(), opts, textOpts(), svc); err != nil {
		t.Fatalf("runAsk: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hello") {
		t.Errorf("answer body missing: %q", got)
	}
	if !strings.Contains(got, "Tool trace") {
		t.Errorf("tool trace footer missing: %q", got)
	}
}

// TestSessionAsk_FormatNDJSON_PassthroughsSDKEvents verifies:
// 1 init line + N SDK events = N+1 total lines; first is init, rest are SDK events.
func TestSessionAsk_FormatNDJSON_PassthroughsSDKEvents(t *testing.T) {
	// Fake stream emits 3 events: tool_call, answer, done.
	// With the init injection, total output is 4 lines (1 init + 3 SDK events).
	svc := &scriptedAskSvc{
		events: []*sdk.AgentStreamResponse{
			toolCallEvent("call_1", "knowledge_search"),
			answerEvent("hello"),
			doneEvent(),
		},
	}

	out, _ := iostreams.SetForTest(t)

	opts := &AskOptions{AgentID: "ag_x", Query: "hi"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatNDJSON}
	if err := runAsk(context.Background(), opts, fopts, svc); err != nil {
		t.Fatalf("runAsk: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	// 1 init + 3 SDK events = 4 lines.
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), out.String())
	}
	// Each line must be valid JSON.
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n  %s", i+1, err, line)
		}
	}

	// First line: CLI-injected init event.
	var initLine map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initLine); err != nil {
		t.Fatalf("line 1 (init) not JSON: %v", err)
	}
	if initLine["type"] != "init" {
		t.Errorf("first line type=%v, want init", initLine["type"])
	}
	if initLine["agent_id"] != "ag_x" {
		t.Errorf("first line agent_id=%v, want ag_x", initLine["agent_id"])
	}

	// Second line: tool_call event (SDK passthrough).
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("line 2 not JSON: %v", err)
	}
	if second["response_type"] != string(sdk.AgentResponseTypeToolCall) {
		t.Errorf("second event response_type=%v, want %s", second["response_type"], sdk.AgentResponseTypeToolCall)
	}
	// Third line: answer event.
	var third map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &third); err != nil {
		t.Fatalf("line 3 not JSON: %v", err)
	}
	if third["response_type"] != string(sdk.AgentResponseTypeAnswer) {
		t.Errorf("third event response_type=%v, want %s", third["response_type"], sdk.AgentResponseTypeAnswer)
	}
	// Fourth line: done event.
	var fourth map[string]any
	if err := json.Unmarshal([]byte(lines[3]), &fourth); err != nil {
		t.Fatalf("line 4 not JSON: %v", err)
	}
	if fourth["done"] != true {
		t.Errorf("fourth event done=%v, want true", fourth["done"])
	}
}

func TestSessionAsk_RequiresAgentFlag(t *testing.T) {
	// Build the real cobra command with a nil factory — flag parsing happens
	// before RunE so the factory is never dereferenced for this test.
	f := &cmdutil.Factory{}
	cmd := NewCmdAsk(f)
	// Redirect output to discard cobra error messages.
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// Execute without --agent: cobra should refuse with exit-code 2.
	cmd.SetArgs([]string{"some question"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --agent is missing, got nil")
	}
	// Cobra wraps required-flag errors; the message should mention the flag.
	if !strings.Contains(err.Error(), "agent") {
		t.Errorf("error should mention 'agent' flag, got: %v", err)
	}
}

// TestSessionAsk_NDJSON_IncludesReferencesViaSDKEvent verifies that references
// emitted by the SDK appear as passthrough NDJSON lines (not lost).
func TestSessionAsk_NDJSON_IncludesReferencesViaSDKEvent(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &scriptedAskSvc{
		events: []*sdk.AgentStreamResponse{
			answerEvent("Hello world."),
			referencesEvent([]*sdk.SearchResult{{KnowledgeID: "k1", KnowledgeTitle: "Doc 1"}}),
			doneEvent(),
		},
	}
	opts := &AskOptions{AgentID: "ag_x", Query: "ping"}
	if err := runAsk(context.Background(), opts, jsonOpts(), svc); err != nil {
		t.Fatalf("runAsk: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	// 1 init + 3 SDK events = 4 lines.
	if len(lines) != 4 {
		t.Fatalf("got %d NDJSON lines, want 4:\n%s", len(lines), out.String())
	}
	// init line.
	var init map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &init); err != nil {
		t.Fatalf("line 0 not JSON: %v", err)
	}
	if init["type"] != "init" {
		t.Errorf("line 0 type: got %v, want init", init["type"])
	}
	// references line is the third SDK event (lines[3] = index 3).
	var refsLine map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &refsLine); err != nil {
		t.Fatalf("references line not JSON: %v", err)
	}
	if refsLine["response_type"] != string(sdk.AgentResponseTypeReferences) {
		t.Errorf("expected references event at line 3, got response_type=%v", refsLine["response_type"])
	}
}
