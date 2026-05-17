package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
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

// textOpts returns a FormatOptions configured for the text (human) render
// path — the most common shape under test.
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

func TestChat_JSONMode(t *testing.T) {
	out, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{
			{Content: "answer body"},
			{AssistantMessageID: "msg_99"},
			{ResponseType: sdk.ResponseTypeReferences, KnowledgeReferences: []*sdk.SearchResult{{KnowledgeID: "k1"}}},
			{ResponseType: sdk.ResponseTypeComplete, Done: true},
		},
	}
	opts := &Options{Query: "q", KBID: "kb_42"}
	if err := runChat(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc); err != nil {
		t.Fatalf("runChat: %v", err)
	}

	// JSON mode must NOT print the human session-hint on stderr; the session
	// id is carried inside the JSON object instead.
	if errBuf.Len() != 0 {
		t.Errorf("expected empty stderr in JSON mode, got %q", errBuf.String())
	}

	var got struct {
		Answer             string `json:"answer"`
		SessionID          string `json:"session_id"`
		AssistantMessageID string `json:"assistant_message_id"`
		KBID               string `json:"kb_id"`
		Query              string `json:"query"`
		References         []struct {
			KnowledgeID string `json:"knowledge_id"`
		} `json:"references"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, out.String())
	}
	if got.Answer != "answer body" {
		t.Errorf("answer: got %q", got.Answer)
	}
	if got.SessionID != "sess_auto" {
		t.Errorf("session_id: got %q", got.SessionID)
	}
	if got.AssistantMessageID != "msg_99" {
		t.Errorf("assistant_message_id: got %q", got.AssistantMessageID)
	}
	if got.KBID != "kb_42" {
		t.Errorf("kb_id: got %q", got.KBID)
	}
	if got.Query != "q" {
		t.Errorf("query: got %q", got.Query)
	}
	if len(got.References) != 1 || got.References[0].KnowledgeID != "k1" {
		t.Errorf("references payload missing: %+v", got.References)
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

func TestChat_FormatNDJSON_PassthroughsSDKEvents(t *testing.T) {
	// Fake stream emits 3 events: thinking, answer, complete.
	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{
			{ResponseType: sdk.ResponseTypeThinking, Content: "search KB"},
			{ResponseType: sdk.ResponseTypeAnswer, Content: "hello"},
			{ResponseType: sdk.ResponseTypeComplete, Done: true, SessionID: "sess_x"},
		},
	}
	var stdout bytes.Buffer
	prev := iostreams.IO.Out
	iostreams.IO.Out = &stdout
	defer func() { iostreams.IO.Out = prev }()
	// Also redirect stderr so the auto-created session hint doesn't write to
	// real stderr during tests.
	prevErr := iostreams.IO.Err
	iostreams.IO.Err = os.Stderr
	defer func() { iostreams.IO.Err = prevErr }()

	opts := &Options{Query: "hi", KBID: "kb_x"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatNDJSON}
	if err := runChat(context.Background(), opts, fopts, svc); err != nil {
		t.Fatalf("runChat: %v", err)
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), stdout.String())
	}
	// Each line must be valid JSON with the right response_type.
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 1 not JSON: %v", err)
	}
	if first["response_type"] != "thinking" {
		t.Errorf("first event response_type=%v, want thinking", first["response_type"])
	}
	// Second line: answer.
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("line 2 not JSON: %v", err)
	}
	if second["response_type"] != "answer" {
		t.Errorf("second event response_type=%v, want answer", second["response_type"])
	}
	// Third line: complete with done=true.
	var third map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &third); err != nil {
		t.Fatalf("line 3 not JSON: %v", err)
	}
	if third["done"] != true {
		t.Errorf("third event done=%v, want true", third["done"])
	}
}
