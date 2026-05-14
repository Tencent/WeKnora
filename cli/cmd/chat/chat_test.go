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

// fakeChatService implements chatService for unit tests. Tests configure the
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
	// envelope assertions don't depend on uuid generation.
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

// Sanity: fakeChatService must satisfy chatService. Mirrors the production
// var _ chatService = (*sdk.Client)(nil) check at the bottom of chat.go.
var _ chatService = (*fakeChatService)(nil)

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
	if err := runChat(context.Background(), opts, nil, svc); err != nil {
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
	if err := runChat(context.Background(), opts, &cmdutil.JSONOptions{}, svc); err != nil {
		t.Fatalf("runChat: %v", err)
	}

	// JSON mode must NOT print the human session-hint on stderr; the session
	// id is carried inside the envelope instead.
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

func TestChat_NoStreamFlag(t *testing.T) {
	// TTY enabled, but --no-stream forces accumulate mode → no live writes
	// during callback; entire answer printed once after Done.
	out, _ := iostreams.SetForTestWithTTY(t)
	var written string
	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{
			{ResponseType: sdk.ResponseTypeAnswer, Content: "buffered "},
			{ResponseType: sdk.ResponseTypeAnswer, Content: "answer"},
			{ResponseType: sdk.ResponseTypeComplete, Done: true},
		},
	}
	opts := &Options{Query: "q", KBID: "kb", NoStream: true}
	if err := runChat(context.Background(), opts, nil, svc); err != nil {
		t.Fatalf("runChat: %v", err)
	}
	written = out.String()
	if !strings.Contains(written, "buffered answer") {
		t.Errorf("expected concatenated answer, got %q", written)
	}
}

func TestChat_NonTTY_AccumulateMode(t *testing.T) {
	// Non-TTY iostreams forces accumulate mode even without --no-stream.
	out, _ := iostreams.SetForTest(t)
	svc := &fakeChatService{
		streamEvents: []*sdk.StreamResponse{
			{ResponseType: sdk.ResponseTypeAnswer, Content: "piped"},
			{ResponseType: sdk.ResponseTypeComplete, Done: true},
		},
	}
	opts := &Options{Query: "q", KBID: "kb"}
	if err := runChat(context.Background(), opts, nil, svc); err != nil {
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
	if err := runChat(context.Background(), opts, nil, svc); err != nil {
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
	err := runChat(context.Background(), opts, nil, svc)
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
	err := runChat(context.Background(), opts, nil, svc)
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
	err := runChat(context.Background(), opts, nil, svc)
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
	err := runChat(context.Background(), opts, nil, svc)
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
	err := runChat(ctx, opts, nil, svc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if typed.Code != cmdutil.CodeUserAborted {
		t.Errorf("code: got %q want %q", typed.Code, cmdutil.CodeUserAborted)
	}
}

func TestChat_SessionCreateFails(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeChatService{
		createSessionErr: errors.New("dial tcp: connection refused"),
	}
	opts := &Options{Query: "q", KBID: "kb"}
	err := runChat(context.Background(), opts, nil, svc)
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
	err := runChat(context.Background(), opts, nil, svc)
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
