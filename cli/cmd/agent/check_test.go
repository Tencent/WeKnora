package agentcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeAgentCheckSvc struct {
	agent     *sdk.Agent
	agentErr  error
	kbProbeOK map[string]bool // kb_id → reachable
	kbErr     error           // applied for any KB id not in the map
}

func (f *fakeAgentCheckSvc) GetAgent(_ context.Context, id string) (*sdk.Agent, error) {
	if f.agentErr != nil {
		return nil, f.agentErr
	}
	return f.agent, nil
}

func (f *fakeAgentCheckSvc) GetKnowledgeBase(_ context.Context, id string) (*sdk.KnowledgeBase, error) {
	if ok, found := f.kbProbeOK[id]; found {
		if !ok {
			return nil, fmt.Errorf("kb %s unreachable", id)
		}
		return &sdk.KnowledgeBase{ID: id}, nil
	}
	if f.kbErr != nil {
		return nil, f.kbErr
	}
	return &sdk.KnowledgeBase{ID: id}, nil
}

func TestRunAgentCheck_AllReachable(t *testing.T) {
	svc := &fakeAgentCheckSvc{
		agent: &sdk.Agent{ID: "ag_x", Config: &sdk.AgentConfig{
			ModelID:        "m_x",
			KnowledgeBases: []string{"kb_a", "kb_b"},
		}},
		kbProbeOK: map[string]bool{"kb_a": true, "kb_b": true},
	}
	res, err := runAgentCheck(context.Background(), svc, "ag_x")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !res.Reachable {
		t.Error("Reachable=false, want true")
	}
	if res.ModelID != "m_x" {
		t.Errorf("ModelID=%q, want m_x", res.ModelID)
	}
	if res.KBScopeAllReachable == nil || !*res.KBScopeAllReachable {
		t.Errorf("KBScopeAllReachable should be true (pointer set), got %v", res.KBScopeAllReachable)
	}
}

func TestRunAgentCheck_OneKBFails(t *testing.T) {
	svc := &fakeAgentCheckSvc{
		agent: &sdk.Agent{ID: "ag_x", Config: &sdk.AgentConfig{
			ModelID:        "m_x",
			KnowledgeBases: []string{"kb_a", "kb_b"},
		}},
		kbProbeOK: map[string]bool{"kb_a": true, "kb_b": false},
	}
	res, _ := runAgentCheck(context.Background(), svc, "ag_x")
	if res.KBScopeAllReachable == nil || *res.KBScopeAllReachable {
		t.Errorf("KBScopeAllReachable should be false; got %v", res.KBScopeAllReachable)
	}
}

func TestRunAgentCheck_Unreachable(t *testing.T) {
	svc := &fakeAgentCheckSvc{agentErr: fmt.Errorf("404")}
	res, err := runAgentCheck(context.Background(), svc, "ag_x")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if res.Reachable {
		t.Error("Reachable=true on 404; want false")
	}
	if res.ID != "ag_x" {
		t.Errorf("ID=%q, want ag_x (echoed even on unreachable)", res.ID)
	}
	// KBScopeAllReachable should be nil when agent is unreachable
	if res.KBScopeAllReachable != nil {
		t.Errorf("KBScopeAllReachable should be nil when agent unreachable; got %v", res.KBScopeAllReachable)
	}
}

func TestRunAgentCheck_NilConfig(t *testing.T) {
	// Defensive: Agent.Config is a pointer; nil should not panic and
	// KBScopeAllReachable should be vacuously true (no KBs to probe).
	svc := &fakeAgentCheckSvc{agent: &sdk.Agent{ID: "ag_x", Config: nil}}
	res, err := runAgentCheck(context.Background(), svc, "ag_x")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !res.Reachable {
		t.Error("Reachable=false, want true on nil config")
	}
	if res.ModelID != "" {
		t.Errorf("ModelID=%q, want empty (no config)", res.ModelID)
	}
	// Vacuously true: no KBs to probe
	if res.KBScopeAllReachable == nil || !*res.KBScopeAllReachable {
		t.Errorf("KBScopeAllReachable should be vacuously true for nil config; got %v", res.KBScopeAllReachable)
	}
}

func TestEmitAgentCheck_JSON(t *testing.T) {
	trueP := true
	var buf bytes.Buffer
	res := &AgentCheckResult{ID: "ag_x", Reachable: true, ModelID: "m_x", KBScopeAllReachable: &trueP}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	if err := emitAgentCheck(res, fopts, &buf); err != nil {
		t.Fatalf("%v", err)
	}
	var env struct {
		OK   bool             `json:"ok"`
		Data AgentCheckResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("%v", err)
	}
	got := env.Data
	if got.ModelID != "m_x" {
		t.Errorf("ModelID=%q, want m_x", got.ModelID)
	}
	if got.KBScopeAllReachable == nil || !*got.KBScopeAllReachable {
		t.Errorf("KBScopeAllReachable should be true in JSON output; got %v", got.KBScopeAllReachable)
	}
}

func TestEmitAgentCheck_TextHuman(t *testing.T) {
	trueP := true
	var buf bytes.Buffer
	res := &AgentCheckResult{ID: "ag_x", Reachable: true, ModelID: "m_x", KBScopeAllReachable: &trueP}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}
	if err := emitAgentCheck(res, fopts, &buf); err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"ag_x", "m_x", "true"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	}
}
