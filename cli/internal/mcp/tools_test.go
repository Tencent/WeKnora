package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	sdk "github.com/Tencent/WeKnora/client"
)

// fakeSvc implements every narrow service interface ServiceClient embeds.
// Each method records the last call args; per-test setup populates the
// return values it wants to assert against.
type fakeSvc struct {
	listKBs        []sdk.KnowledgeBase
	listKBsErr     error
	getKB          *sdk.KnowledgeBase
	getKBErr       error
	listDocs       []sdk.Knowledge
	listDocsTotal  int64
	listDocsErr    error
	getDoc         *sdk.Knowledge
	getDocErr      error
	openDocName    string
	openDocBody    io.ReadCloser
	openDocErr     error
	hybridResults  []*sdk.SearchResult
	hybridErr      error
	createSess     *sdk.Session
	createSessErr  error
	kbStreamEvents []*sdk.StreamResponse
	kbStreamErr    error
	agents         []sdk.Agent
	agentsErr      error
	agent          *sdk.Agent
	agentErr       error
	agentEvents    []*sdk.AgentStreamResponse
	agentStreamErr error
	// Captured args:
	calls struct {
		listKBs       int
		kbViewID      string
		docListKBID   string
		docListFilter sdk.KnowledgeListFilter
		docViewID     string
		openDocID     string
		hybridKBID    string
		hybridParams  *sdk.SearchParams
		createSessReq *sdk.CreateSessionRequest
		kbQAReq       *sdk.KnowledgeQARequest
		kbQASess      string
		agentListN    int
		agentViewID   string
		agentReq      *sdk.AgentQARequest
		agentSess     string
	}
}

func (f *fakeSvc) ListKnowledgeBases(_ context.Context) ([]sdk.KnowledgeBase, error) {
	f.calls.listKBs++
	return f.listKBs, f.listKBsErr
}
func (f *fakeSvc) GetKnowledgeBase(_ context.Context, id string) (*sdk.KnowledgeBase, error) {
	f.calls.kbViewID = id
	return f.getKB, f.getKBErr
}
func (f *fakeSvc) ListKnowledgeWithFilter(_ context.Context, kbID string, _, _ int, filter sdk.KnowledgeListFilter) ([]sdk.Knowledge, int64, error) {
	f.calls.docListKBID = kbID
	f.calls.docListFilter = filter
	return f.listDocs, f.listDocsTotal, f.listDocsErr
}
func (f *fakeSvc) GetKnowledge(_ context.Context, id string) (*sdk.Knowledge, error) {
	f.calls.docViewID = id
	return f.getDoc, f.getDocErr
}
func (f *fakeSvc) OpenKnowledgeFile(_ context.Context, id string) (string, io.ReadCloser, error) {
	f.calls.openDocID = id
	return f.openDocName, f.openDocBody, f.openDocErr
}
func (f *fakeSvc) HybridSearch(_ context.Context, kbID string, p *sdk.SearchParams) ([]*sdk.SearchResult, error) {
	f.calls.hybridKBID, f.calls.hybridParams = kbID, p
	return f.hybridResults, f.hybridErr
}
func (f *fakeSvc) CreateSession(_ context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error) {
	f.calls.createSessReq = req
	if f.createSess == nil && f.createSessErr == nil {
		return &sdk.Session{ID: "sess_auto"}, nil
	}
	return f.createSess, f.createSessErr
}
func (f *fakeSvc) KnowledgeQAStream(_ context.Context, sess string, req *sdk.KnowledgeQARequest, cb func(*sdk.StreamResponse) error) error {
	f.calls.kbQASess, f.calls.kbQAReq = sess, req
	for _, e := range f.kbStreamEvents {
		if err := cb(e); err != nil {
			return err
		}
	}
	return f.kbStreamErr
}
func (f *fakeSvc) ListAgents(_ context.Context) ([]sdk.Agent, error) {
	f.calls.agentListN++
	return f.agents, f.agentsErr
}
func (f *fakeSvc) GetAgent(_ context.Context, id string) (*sdk.Agent, error) {
	f.calls.agentViewID = id
	return f.agent, f.agentErr
}
func (f *fakeSvc) AgentQAStreamWithRequest(_ context.Context, sess string, req *sdk.AgentQARequest, cb sdk.AgentEventCallback) error {
	f.calls.agentSess, f.calls.agentReq = sess, req
	for _, e := range f.agentEvents {
		if err := cb(e); err != nil {
			return err
		}
	}
	return f.agentStreamErr
}

// newTestServer wires svc to an in-process MCP server and returns a
// connected client session ready to CallTool against it.
func newTestServer(t *testing.T, svc ServiceClient) (*mcpsdk.ClientSession, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "weknora-test", Version: "v0.0.0-test"}, nil)
	registerTools(server, svc)

	st, ct := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		cancel()
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		_ = serverSession.Close()
		cancel()
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
		cancel()
	})
	return clientSession, cancel
}

// callTool invokes name with args and returns the parsed structured output.
func callTool(t *testing.T, c *mcpsdk.ClientSession, name string, args any, out any) *mcpsdk.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.IsError {
		if len(res.Content) > 0 {
			t.Fatalf("tool %s returned error: %+v", name, res.Content)
		}
		t.Fatalf("tool %s returned error (no content)", name)
	}
	if out != nil && res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("decode %s output: %v\nraw=%s", name, err, b)
		}
	}
	return res
}

func TestTool_ListsRegistered(t *testing.T) {
	c, _ := newTestServer(t, &fakeSvc{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := []string{"kb_list", "kb_view", "doc_list", "doc_view", "doc_download", "search_chunks", "chat", "agent_list", "agent_invoke"}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool %q in ListTools response", name)
		}
	}
	if len(res.Tools) != len(want) {
		t.Errorf("registered %d tools, want exactly %d (no scope creep)", len(res.Tools), len(want))
	}
}

func TestTool_KBList(t *testing.T) {
	svc := &fakeSvc{listKBs: []sdk.KnowledgeBase{{ID: "kb1", Name: "Marketing"}}}
	c, _ := newTestServer(t, svc)
	var out kbListOutput
	callTool(t, c, "kb_list", map[string]any{}, &out)
	if len(out.Items) != 1 || out.Items[0].ID != "kb1" {
		t.Errorf("got %+v", out)
	}
}

func TestTool_KBView_RequiresKBID(t *testing.T) {
	c, _ := newTestServer(t, &fakeSvc{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: "kb_view", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true on missing kb_id")
	}
}

func TestTool_KBView(t *testing.T) {
	svc := &fakeSvc{getKB: &sdk.KnowledgeBase{ID: "kb_x", Name: "Eng"}}
	c, _ := newTestServer(t, svc)
	var out sdk.KnowledgeBase
	callTool(t, c, "kb_view", map[string]any{"kb_id": "kb_x"}, &out)
	if out.ID != "kb_x" || out.Name != "Eng" {
		t.Errorf("got %+v", out)
	}
	if svc.calls.kbViewID != "kb_x" {
		t.Errorf("kb_id not forwarded: %s", svc.calls.kbViewID)
	}
}

func TestTool_DocList_DefaultPagination(t *testing.T) {
	svc := &fakeSvc{listDocs: []sdk.Knowledge{{ID: "k1"}}, listDocsTotal: 1}
	c, _ := newTestServer(t, svc)
	var out docListOutput
	callTool(t, c, "doc_list", map[string]any{"kb_id": "kb_x"}, &out)
	if out.Page != 1 || out.PageSize != 20 {
		t.Errorf("default pagination not applied: %+v", out)
	}
	if svc.calls.docListKBID != "kb_x" {
		t.Errorf("kb_id not forwarded: %s", svc.calls.docListKBID)
	}
}

func TestTool_DocList_StatusFilter_Forwarded(t *testing.T) {
	svc := &fakeSvc{}
	c, _ := newTestServer(t, svc)
	callTool(t, c, "doc_list", map[string]any{"kb_id": "kb_x", "status": "failed"}, nil)
	if svc.calls.docListFilter.ParseStatus != "failed" {
		t.Errorf("status not forwarded as filter.ParseStatus: %+v", svc.calls.docListFilter)
	}
}

func TestTool_DocView(t *testing.T) {
	svc := &fakeSvc{getDoc: &sdk.Knowledge{ID: "k1", FileName: "a.pdf"}}
	c, _ := newTestServer(t, svc)
	var out sdk.Knowledge
	callTool(t, c, "doc_view", map[string]any{"knowledge_id": "k1"}, &out)
	if out.ID != "k1" {
		t.Errorf("got %+v", out)
	}
}

func TestTool_DocDownload_Text(t *testing.T) {
	svc := &fakeSvc{
		openDocName: "notes.txt",
		openDocBody: io.NopCloser(strings.NewReader("hello world")),
	}
	c, _ := newTestServer(t, svc)
	var out docDownloadOutput
	callTool(t, c, "doc_download", map[string]any{"knowledge_id": "k1"}, &out)
	if out.Content != "hello world" {
		t.Errorf("content = %q", out.Content)
	}
	if out.IsBase64 {
		t.Error("text content should not be base64-encoded")
	}
}

func TestTool_DocDownload_BinaryBase64(t *testing.T) {
	// First 512 bytes contain a NUL → encodeDownload returns base64.
	bin := []byte{0x00, 0x01, 0x02, 0x03}
	svc := &fakeSvc{
		openDocName: "blob.bin",
		openDocBody: io.NopCloser(strings.NewReader(string(bin))),
	}
	c, _ := newTestServer(t, svc)
	var out docDownloadOutput
	callTool(t, c, "doc_download", map[string]any{"knowledge_id": "k1"}, &out)
	if !out.IsBase64 {
		t.Errorf("binary should be base64; got is_base64=%v content=%q", out.IsBase64, out.Content)
	}
}

func TestTool_SearchChunks(t *testing.T) {
	svc := &fakeSvc{hybridResults: []*sdk.SearchResult{{KnowledgeID: "k1", Score: 0.9}}}
	c, _ := newTestServer(t, svc)
	var out searchChunksOutput
	callTool(t, c, "search_chunks", map[string]any{"kb_id": "kb_x", "query": "what is RAG"}, &out)
	if len(out.Results) != 1 || out.Results[0].KnowledgeID != "k1" {
		t.Errorf("got %+v", out)
	}
}

func TestTool_SearchChunks_LimitCap(t *testing.T) {
	// 5 results, limit 3 → 3 returned.
	svc := &fakeSvc{}
	for i := 0; i < 5; i++ {
		svc.hybridResults = append(svc.hybridResults, &sdk.SearchResult{KnowledgeID: "k", Score: float64(i)})
	}
	c, _ := newTestServer(t, svc)
	var out searchChunksOutput
	callTool(t, c, "search_chunks", map[string]any{"kb_id": "kb_x", "query": "x", "limit": 3}, &out)
	if len(out.Results) != 3 {
		t.Errorf("limit not honored: got %d, want 3", len(out.Results))
	}
}

func TestTool_Chat_AccumulateAnswerAndReferences(t *testing.T) {
	svc := &fakeSvc{
		kbStreamEvents: []*sdk.StreamResponse{
			{Content: "Hello "},
			{Content: "world."},
			{KnowledgeReferences: []*sdk.SearchResult{{KnowledgeID: "k1"}}},
			{ResponseType: sdk.ResponseTypeComplete},
		},
	}
	c, _ := newTestServer(t, svc)
	var out chatOutput
	callTool(t, c, "chat", map[string]any{"kb_id": "kb_x", "query": "ping"}, &out)
	if out.Answer != "Hello world." {
		t.Errorf("answer = %q", out.Answer)
	}
	if len(out.References) != 1 || out.References[0].KnowledgeID != "k1" {
		t.Errorf("references missing: %+v", out.References)
	}
	if out.SessionID != "sess_auto" {
		t.Errorf("session_id = %q, want sess_auto", out.SessionID)
	}
}

func TestTool_Chat_ExistingSessionSkipsCreate(t *testing.T) {
	svc := &fakeSvc{
		kbStreamEvents: []*sdk.StreamResponse{{ResponseType: sdk.ResponseTypeComplete}},
	}
	c, _ := newTestServer(t, svc)
	callTool(t, c, "chat", map[string]any{"kb_id": "kb_x", "query": "x", "session_id": "sess_existing"}, nil)
	if svc.calls.createSessReq != nil {
		t.Error("CreateSession should not fire when session_id is supplied")
	}
	if svc.calls.kbQASess != "sess_existing" {
		t.Errorf("session id not forwarded to QA stream: %s", svc.calls.kbQASess)
	}
}

func TestTool_AgentList(t *testing.T) {
	svc := &fakeSvc{agents: []sdk.Agent{{ID: "ag1", Name: "Research"}}}
	c, _ := newTestServer(t, svc)
	var out agentListOutput
	callTool(t, c, "agent_list", map[string]any{}, &out)
	if len(out.Items) != 1 || out.Items[0].ID != "ag1" {
		t.Errorf("got %+v", out)
	}
}

func TestTool_AgentInvoke(t *testing.T) {
	svc := &fakeSvc{
		agentEvents: []*sdk.AgentStreamResponse{
			{ResponseType: sdk.AgentResponseTypeAnswer, Content: "result"},
			{ResponseType: sdk.AgentResponseTypeToolCall, ID: "c1", Content: "knowledge_search"},
			{Done: true},
		},
	}
	c, _ := newTestServer(t, svc)
	var out agentInvokeOutput
	callTool(t, c, "agent_invoke", map[string]any{"agent_id": "ag1", "query": "x"}, &out)
	if out.Answer != "result" {
		t.Errorf("answer = %q", out.Answer)
	}
	if len(out.ToolEvents) != 1 {
		t.Errorf("tool_calls len = %d, want 1", len(out.ToolEvents))
	}
	if out.AgentID != "ag1" {
		t.Errorf("agent_id = %q", out.AgentID)
	}
}

func TestTool_AgentInvoke_StreamAbort(t *testing.T) {
	svc := &fakeSvc{
		agentEvents:    []*sdk.AgentStreamResponse{{ResponseType: sdk.AgentResponseTypeAnswer, Content: "partial"}},
		agentStreamErr: errors.New("connection reset"),
	}
	c, _ := newTestServer(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: "agent_invoke", Arguments: map[string]any{"agent_id": "ag1", "query": "x"}})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true on mid-stream abort")
	}
}
