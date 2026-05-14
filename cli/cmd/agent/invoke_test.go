package agentcmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// scriptedInvokeSvc serves a canned stream of agent events to runInvoke.
type scriptedInvokeSvc struct {
	createResp *sdk.Session
	createErr  error
	events     []*sdk.AgentStreamResponse
	streamErr  error
	got        struct {
		sessionID string
		req       *sdk.AgentQARequest
	}
}

func (s *scriptedInvokeSvc) CreateSession(_ context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error) {
	if s.createResp == nil && s.createErr == nil {
		return &sdk.Session{ID: "sess_auto", Title: req.Title}, nil
	}
	return s.createResp, s.createErr
}

func (s *scriptedInvokeSvc) AgentQAStreamWithRequest(_ context.Context, sessionID string, req *sdk.AgentQARequest, cb sdk.AgentEventCallback) error {
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

func TestInvoke_AccumulateMode_EmitsBareJSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &scriptedInvokeSvc{
		events: []*sdk.AgentStreamResponse{
			answerEvent("Hello "),
			answerEvent("world."),
			referencesEvent([]*sdk.SearchResult{{KnowledgeID: "k1", KnowledgeTitle: "Doc 1"}}),
			doneEvent(),
		},
	}
	opts := &InvokeOptions{AgentID: "ag_x", Query: "ping"}
	if err := runInvoke(context.Background(), opts, &cmdutil.JSONOptions{}, svc); err != nil {
		t.Fatalf("runInvoke: %v", err)
	}
	var got invokeData
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, out.String())
	}
	if got.Answer != "Hello world." {
		t.Errorf("answer = %q, want %q", got.Answer, "Hello world.")
	}
	if got.AgentID != "ag_x" {
		t.Errorf("agent_id = %q, want ag_x", got.AgentID)
	}
	if got.Query != "ping" {
		t.Errorf("query = %q, want ping", got.Query)
	}
	if got.SessionID != "sess_auto" {
		t.Errorf("session_id = %q, want sess_auto", got.SessionID)
	}
	if len(got.References) != 1 || got.References[0].KnowledgeID != "k1" {
		t.Errorf("references missing: %+v", got.References)
	}
}

func TestInvoke_AutoCreatedSessionID_PassedAsAgentRequest(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedInvokeSvc{events: []*sdk.AgentStreamResponse{doneEvent()}}
	opts := &InvokeOptions{AgentID: "ag_42", Query: "x"}
	if err := runInvoke(context.Background(), opts, &cmdutil.JSONOptions{}, svc); err != nil {
		t.Fatalf("runInvoke: %v", err)
	}
	if svc.got.sessionID != "sess_auto" {
		t.Errorf("agent-chat got sessionID=%q, want sess_auto", svc.got.sessionID)
	}
	if svc.got.req == nil || svc.got.req.AgentID != "ag_42" {
		t.Errorf("AgentID not forwarded: %+v", svc.got.req)
	}
	if !svc.got.req.AgentEnabled {
		t.Error("AgentEnabled must be true for agent invoke")
	}
}

func TestInvoke_ExistingSessionID_SkipsCreate(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	created := false
	svc := &scriptedInvokeSvc{events: []*sdk.AgentStreamResponse{doneEvent()}}
	// Wrap CreateSession to detect call.
	svc.createResp = &sdk.Session{ID: "should_not_be_used"}
	wrapped := &createSessionTracker{InvokeService: svc, called: &created}
	opts := &InvokeOptions{AgentID: "ag", Query: "x", SessionID: "sess_existing"}
	if err := runInvoke(context.Background(), opts, &cmdutil.JSONOptions{}, wrapped); err != nil {
		t.Fatalf("runInvoke: %v", err)
	}
	if created {
		t.Error("CreateSession should not be called when --session is set")
	}
	if svc.got.sessionID != "sess_existing" {
		t.Errorf("agent-chat got sessionID=%q, want sess_existing", svc.got.sessionID)
	}
}

type createSessionTracker struct {
	InvokeService
	called *bool
}

func (c *createSessionTracker) CreateSession(ctx context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error) {
	*c.called = true
	return c.InvokeService.CreateSession(ctx, req)
}

func TestInvoke_ToolEventsCaptured(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &scriptedInvokeSvc{events: []*sdk.AgentStreamResponse{
		toolCallEvent("call_1", "knowledge_search"),
		answerEvent("answer text"),
		doneEvent(),
	}}
	opts := &InvokeOptions{AgentID: "ag", Query: "x"}
	if err := runInvoke(context.Background(), opts, &cmdutil.JSONOptions{}, svc); err != nil {
		t.Fatalf("runInvoke: %v", err)
	}
	var got invokeData
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.ToolEvents) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(got.ToolEvents))
	}
	if got.ToolEvents[0].ID != "call_1" {
		t.Errorf("tool_calls[0].id = %q, want call_1", got.ToolEvents[0].ID)
	}
}

func TestInvoke_EmptyQuery_Rejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedInvokeSvc{}
	opts := &InvokeOptions{AgentID: "ag", Query: ""}
	err := runInvoke(context.Background(), opts, nil, svc)
	if err == nil {
		t.Fatal("expected input.invalid_argument, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("expected input.invalid_argument, got %v", err)
	}
}

func TestInvoke_StreamAbortBeforeDone_MapsToSSEStreamAborted(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedInvokeSvc{
		events: []*sdk.AgentStreamResponse{
			answerEvent("partial"),
		},
		streamErr: errors.New("connection reset"),
	}
	opts := &InvokeOptions{AgentID: "ag", Query: "x"}
	err := runInvoke(context.Background(), opts, &cmdutil.JSONOptions{}, svc)
	if err == nil {
		t.Fatal("expected stream-aborted error")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeSSEStreamAborted {
		t.Errorf("expected local.sse_stream_aborted, got %v", err)
	}
}

func TestInvoke_NoDoneEvent_MapsToSSEStreamAborted(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedInvokeSvc{events: []*sdk.AgentStreamResponse{answerEvent("incomplete")}}
	opts := &InvokeOptions{AgentID: "ag", Query: "x"}
	err := runInvoke(context.Background(), opts, &cmdutil.JSONOptions{}, svc)
	if err == nil {
		t.Fatal("expected stream-aborted error")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeSSEStreamAborted {
		t.Errorf("expected local.sse_stream_aborted, got %v", err)
	}
}

func TestInvoke_CreateSessionFails_MapsToSessionCreateFailed(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &scriptedInvokeSvc{createErr: errors.New("connection refused")}
	opts := &InvokeOptions{AgentID: "ag", Query: "x"}
	err := runInvoke(context.Background(), opts, nil, svc)
	if err == nil {
		t.Fatal("expected session_create_failed")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeSessionCreateFailed {
		t.Errorf("expected server.session_create_failed, got %v", err)
	}
}

func TestInvoke_Cancellation_MapsToUserAborted(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	svc := &scriptedInvokeSvc{streamErr: context.Canceled}
	opts := &InvokeOptions{AgentID: "ag", Query: "x"}
	err := runInvoke(ctx, opts, &cmdutil.JSONOptions{}, svc)
	if err == nil {
		t.Fatal("expected user_aborted")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) || typed.Code != cmdutil.CodeUserAborted {
		t.Errorf("expected local.user_aborted, got %v", err)
	}
}

// Sanity: human-mode output writes the answer body and a tool-trace footer.
func TestInvoke_Human_Accumulate_PrintsAnswerAndFooter(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &scriptedInvokeSvc{events: []*sdk.AgentStreamResponse{
		answerEvent("hello"),
		toolCallEvent("c1", "knowledge_search"),
		doneEvent(),
	}}
	opts := &InvokeOptions{AgentID: "ag", Query: "x", NoStream: true}
	if err := runInvoke(context.Background(), opts, nil, svc); err != nil {
		t.Fatalf("runInvoke: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hello") {
		t.Errorf("answer body missing: %q", got)
	}
	if !strings.Contains(got, "Tool trace") {
		t.Errorf("tool trace footer missing: %q", got)
	}
}
