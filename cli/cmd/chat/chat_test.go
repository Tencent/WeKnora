package chat

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

// fakeChatService implements ChatService for unit tests. Tests configure the
// callback driver via streamEvents (delivered in order) and observe captured
// inputs through the exported fields.
type fakeChatService struct {
	createSessionResp *sdk.Session
	createSessionErr  error
	createCalled      bool

	streamErr      error
	streamEvents   []*sdk.StreamResponse
	gotSessionID   string
	gotRequest     *sdk.KnowledgeQARequest
	streamCalled   bool
	cbReturnsError error // if set, callback aborts after first event with this error
}

func (f *fakeChatService) CreateSession(_ context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error) {
	f.createCalled = true
	if f.createSessionErr != nil {
		return nil, f.createSessionErr
	}
	if f.createSessionResp != nil {
		return f.createSessionResp, nil
	}
	// Default: return a deterministic session id derived from the title so
	// JSON assertions don't depend on uuid generation.
	return &sdk.Session{ID: "sess_auto", Title: req.Title}, nil
}

func (f *fakeChatService) KnowledgeQAStream(ctx context.Context, sessionID string, req *sdk.KnowledgeQARequest, cb func(*sdk.StreamResponse) error) error {
	f.streamCalled = true
	f.gotSessionID = sessionID
	f.gotRequest = req
	for _, ev := range f.streamEvents {
		if err := cb(ev); err != nil {
			return err
		}
		if f.cbReturnsError != nil {
			return f.cbReturnsError
		}
	}
	return f.streamErr
}

// Sanity: fakeChatService must satisfy ChatService. Mirrors the production
// var _ ChatService = (*sdk.Client)(nil) check at the bottom of chat.go.
var _ ChatService = (*fakeChatService)(nil)

// textOpts returns a FormatOptions configured for the text render path —
// the most common shape under test.
func textOpts() *cmdutil.FormatOptions {
	return &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
}

func TestChat_StreamMode(t *testing.T) {
	out, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{
			{ResponseType: sdk.ResponseTypeAnswer, Content: "Hello "},
			{ResponseType: sdk.ResponseTypeAnswer, Content: "world"},
			{ResponseType: sdk.ResponseTypeReferences, KnowledgeReferences: []*sdk.SearchResult{
				{KnowledgeID: "k1", KnowledgeTitle: "Doc One", Score: 0.42},
			}},
			{ResponseType: sdk.ResponseTypeComplete, Done: true},
		},
	}
	opts := &Options{Query: "hi", KBID: "kb_1"}
	if err := runChat(context.Background(), opts, textOpts(), svc); err != nil {
		t.Fatalf("runChat: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Hello world") {
		t.Errorf("stdout missing streamed content: %q", got)
	}
	if !strings.Contains(got, "References") {
		t.Errorf("stdout missing references footer: %q", got)
	}
	if !strings.Contains(got, "Doc One") {
		t.Errorf("references should render KnowledgeTitle, got %q", got)
	}
	// auto-created session must announce itself on stderr
	if !strings.Contains(errBuf.String(), "session: sess_auto") {
		t.Errorf("expected stderr session hint, got %q", errBuf.String())
	}
	if !svc.createCalled {
		t.Error("expected CreateSession invocation when SessionID empty")
	}
	if svc.gotSessionID != "sess_auto" {
		t.Errorf("stream sessionID: got %q want sess_auto", svc.gotSessionID)
	}
	if svc.gotRequest == nil || svc.gotRequest.Channel != "api" {
		t.Errorf("expected Channel=api, got %+v", svc.gotRequest)
	}
}

// TestChat_NDJSON_FirstLineIsInit verifies that the NDJSON path (--format json)
// always injects an "init" line first carrying session_id and kb_id.
func TestChat_NDJSON_FirstLineIsInit(t *testing.T) {
	out, errBuf := iostreams.SetForTest(t)

	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{
			{ResponseType: sdk.ResponseTypeAnswer, Content: "answer"},
			{ResponseType: sdk.ResponseTypeComplete, Done: true},
		},
	}
	opts := &Options{Query: "q", KBID: "kb_42"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	if err := runChat(context.Background(), opts, fopts, svc); err != nil {
		t.Fatalf("runChat: %v", err)
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
		KBID      string `json:"kb_id"`
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
	if first.KBID != "kb_42" {
		t.Errorf("init.kb_id: got %q, want kb_42", first.KBID)
	}
}

// TestChat_NDJSON_PassthroughEvents verifies that the NDJSON path emits
// init + N SDK events = N+1 total lines (no buffering, no extra wrapping).
func TestChat_NDJSON_PassthroughEvents(t *testing.T) {
	out, _ := iostreams.SetForTest(t)

	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{
			{ResponseType: sdk.ResponseTypeAnswer, Content: "hello"},
			{ResponseType: sdk.ResponseTypeComplete, Done: true},
		},
	}
	opts := &Options{Query: "q", KBID: "kb_x"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	if err := runChat(context.Background(), opts, fopts, svc); err != nil {
		t.Fatalf("runChat: %v", err)
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

// TestChat_NDJSON_JSONEqualsNDJSON verifies that --format json and --format ndjson
// produce identical byte output on a streaming command.
func TestChat_NDJSON_JSONEqualsNDJSON(t *testing.T) {
	events := []*sdk.StreamResponse{
		{ResponseType: sdk.ResponseTypeAnswer, Content: "hello"},
		{ResponseType: sdk.ResponseTypeComplete, Done: true},
	}

	runWith := func(mode cmdutil.FormatMode) string {
		out, _ := iostreams.SetForTest(t)
		svc := &fakeChatService{streamEvents: events}
		opts := &Options{Query: "q", KBID: "kb_x"}
		fopts := &cmdutil.FormatOptions{Mode: mode}
		if err := runChat(context.Background(), opts, fopts, svc); err != nil {
			t.Fatalf("runChat(%s): %v", mode, err)
		}
		return out.String()
	}

	jsonOut := runWith(cmdutil.FormatJSON)
	ndjsonOut := runWith(cmdutil.FormatNDJSON)
	if jsonOut != ndjsonOut {
		t.Errorf("--format json and --format ndjson differ:\n  json:   %q\n  ndjson: %q", jsonOut, ndjsonOut)
	}
}

func TestChat_NonTTY_AccumulateMode(t *testing.T) {
	// Non-TTY iostreams forces accumulate mode.
	out, _ := iostreams.SetForTest(t)
	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{
			{ResponseType: sdk.ResponseTypeAnswer, Content: "piped"},
			{ResponseType: sdk.ResponseTypeComplete, Done: true},
		},
	}
	opts := &Options{Query: "q", KBID: "kb"}
	if err := runChat(context.Background(), opts, textOpts(), svc); err != nil {
		t.Fatalf("runChat: %v", err)
	}
	if !strings.Contains(out.String(), "piped") {
		t.Errorf("expected accumulated answer, got %q", out.String())
	}
}

func TestChat_SessionIDProvided(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{{ResponseType: sdk.ResponseTypeComplete, Done: true}},
	}
	opts := &Options{Query: "q", KBID: "kb", SessionID: "sess_existing"}
	if err := runChat(context.Background(), opts, textOpts(), svc); err != nil {
		t.Fatalf("runChat: %v", err)
	}
	if svc.createCalled {
		t.Error("CreateSession must NOT be invoked when --session is provided")
	}
	if svc.gotSessionID != "sess_existing" {
		t.Errorf("stream sessionID: got %q want sess_existing", svc.gotSessionID)
	}
	// No auto-create message because the user supplied the id.
	if strings.Contains(errBuf.String(), "session:") {
		t.Errorf("unexpected session hint emitted with explicit --session: %q", errBuf.String())
	}
}

func TestChat_KBIDRequired(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeChatService{}
	// Run with KBID empty (bypassing the cobra resolver).
	opts := &Options{Query: "q"}
	err := runChat(context.Background(), opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if typed.Code != cmdutil.CodeKBIDRequired {
		t.Errorf("code: got %q want %q", typed.Code, cmdutil.CodeKBIDRequired)
	}
	if svc.createCalled || svc.streamCalled {
		t.Error("KB validation must short-circuit before any SDK call")
	}
}

func TestChat_EmptyQuery(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeChatService{}
	opts := &Options{Query: "", KBID: "kb"}
	err := runChat(context.Background(), opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if typed.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("code: got %q want %q", typed.Code, cmdutil.CodeInputInvalidArgument)
	}
}

func TestChat_SDKError_PreStream(t *testing.T) {
	// SDK fails before any event arrives → ClassifyHTTPError mapping.
	// "HTTP error 401: ..." → auth.unauthenticated.
	_, _ = iostreams.SetForTest(t)
	svc := &fakeChatService{
		streamErr: errors.New("HTTP error 401: token rejected"),
	}
	opts := &Options{Query: "q", KBID: "kb"}
	err := runChat(context.Background(), opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if typed.Code != cmdutil.CodeAuthUnauthenticated {
		t.Errorf("code: got %q want %q", typed.Code, cmdutil.CodeAuthUnauthenticated)
	}
}

func TestChat_SDKError_MidStream_AbortsAsSSE(t *testing.T) {
	// Some content arrived, then the stream errored without a Done event →
	// CodeSSEStreamAborted (separate from generic transport failure).
	_, _ = iostreams.SetForTest(t)
	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{{Content: "partial"}},
		streamErr:    errors.New("connection reset"),
	}
	opts := &Options{Query: "q", KBID: "kb"}
	err := runChat(context.Background(), opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if typed.Code != cmdutil.CodeSSEStreamAborted {
		t.Errorf("code: got %q want %q", typed.Code, cmdutil.CodeSSEStreamAborted)
	}
}

func TestChat_ContextCancelled(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate Ctrl-C delivered before the SDK returns.
	svc := &fakeChatService{streamErr: context.Canceled}
	opts := &Options{Query: "q", KBID: "kb"}
	err := runChat(ctx, opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if typed.Code != cmdutil.CodeOperationCancelled {
		t.Errorf("code: got %q want %q", typed.Code, cmdutil.CodeOperationCancelled)
	}
}

func TestChat_SessionCreateFails(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeChatService{
		createSessionErr: errors.New("dial tcp: connection refused"),
	}
	opts := &Options{Query: "q", KBID: "kb"}
	err := runChat(context.Background(), opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if typed.Code != cmdutil.CodeSessionCreateFailed {
		t.Errorf("code: got %q want %q", typed.Code, cmdutil.CodeSessionCreateFailed)
	}
	if svc.streamCalled {
		t.Error("stream must not be invoked after session creation failed")
	}
}

func TestChat_SessionCreate404SurfacesNotFound(t *testing.T) {
	// HTTP-shaped session-create failures should NOT collapse into the
	// session_create_failed bucket; they keep their canonical mapping so
	// agents can react to e.g. resource.not_found.
	_, _ = iostreams.SetForTest(t)
	svc := &fakeChatService{
		createSessionErr: errors.New("HTTP error 404: tenant not found"),
	}
	opts := &Options{Query: "q", KBID: "kb"}
	err := runChat(context.Background(), opts, textOpts(), svc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if typed.Code != cmdutil.CodeResourceNotFound {
		t.Errorf("code: got %q want %q", typed.Code, cmdutil.CodeResourceNotFound)
	}
}

// TestChat_NDJSON_InitIncludesProfile verifies that when a profile is set,
// the NDJSON init event carries the profile field.
func TestChat_NDJSON_InitIncludesProfile(t *testing.T) {
	cmdutil.SetProfile("prod")
	t.Cleanup(func() { cmdutil.SetProfile("") })

	out, _ := iostreams.SetForTest(t)
	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{
			{ResponseType: sdk.ResponseTypeComplete, Done: true},
		},
	}
	opts := &Options{Query: "q", KBID: "kb_x"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatNDJSON}
	if err := runChat(context.Background(), opts, fopts, svc); err != nil {
		t.Fatalf("runChat: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("no output")
	}
	var initLine struct {
		Type    string `json:"type"`
		Profile string `json:"profile"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &initLine); err != nil {
		t.Fatalf("first line not JSON: %v\n  %s", err, lines[0])
	}
	if initLine.Type != "init" {
		t.Errorf("type: got %q, want init", initLine.Type)
	}
	if initLine.Profile != "prod" {
		t.Errorf("profile: got %q, want prod", initLine.Profile)
	}
}

func TestChat_FormatNDJSON_PassthroughsSDKEvents(t *testing.T) {
	// Fake stream emits 3 events: thinking, answer, complete.
	// With the init injection, total output is 4 lines (1 init + 3 SDK events).
	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{
			{ResponseType: sdk.ResponseTypeThinking, Content: "search KB"},
			{ResponseType: sdk.ResponseTypeAnswer, Content: "hello"},
			{ResponseType: sdk.ResponseTypeComplete, Done: true, SessionID: "sess_x"},
		},
	}
	out, _ := iostreams.SetForTest(t)

	opts := &Options{Query: "hi", KBID: "kb_x"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatNDJSON}
	if err := runChat(context.Background(), opts, fopts, svc); err != nil {
		t.Fatalf("runChat: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	// 1 init + 3 SDK events = 4 lines.
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), out.String())
	}

	// First line: CLI-injected init event.
	var initLine map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initLine); err != nil {
		t.Fatalf("line 1 (init) not JSON: %v", err)
	}
	if initLine["type"] != "init" {
		t.Errorf("first line type=%v, want init", initLine["type"])
	}

	// Second line: thinking event (SDK passthrough).
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("line 2 not JSON: %v", err)
	}
	if second["response_type"] != "thinking" {
		t.Errorf("second event response_type=%v, want thinking", second["response_type"])
	}
	// Third line: answer.
	var third map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &third); err != nil {
		t.Fatalf("line 3 not JSON: %v", err)
	}
	if third["response_type"] != "answer" {
		t.Errorf("third event response_type=%v, want answer", third["response_type"])
	}
	// Fourth line: complete with done=true.
	var fourth map[string]any
	if err := json.Unmarshal([]byte(lines[3]), &fourth); err != nil {
		t.Fatalf("line 4 not JSON: %v", err)
	}
	if fourth["done"] != true {
		t.Errorf("fourth event done=%v, want true", fourth["done"])
	}
}
