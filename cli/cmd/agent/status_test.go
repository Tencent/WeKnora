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

type fakeAgentStatusSvc struct {
	agent    *sdk.Agent
	agentErr error
}

func (f *fakeAgentStatusSvc) GetAgent(_ context.Context, id string) (*sdk.Agent, error) {
	if f.agentErr != nil {
		return nil, f.agentErr
	}
	return f.agent, nil
}

func TestRunAgentStatus_ShallowFields(t *testing.T) {
	svc := &fakeAgentStatusSvc{agent: &sdk.Agent{
		ID: "ag_x",
		Config: &sdk.AgentConfig{
			ModelID:        "m_x",
			KnowledgeBases: []string{"kb_a", "kb_b"},
		},
	}}
	res, err := runAgentStatus(context.Background(), svc, "ag_x")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !res.Reachable {
		t.Error("Reachable=false, want true")
	}
	if res.ModelID != "m_x" {
		t.Errorf("ModelID=%q, want m_x", res.ModelID)
	}
}

func TestRunAgentStatus_Unreachable(t *testing.T) {
	svc := &fakeAgentStatusSvc{agentErr: fmt.Errorf("404")}
	res, err := runAgentStatus(context.Background(), svc, "ag_x")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if res.Reachable {
		t.Error("Reachable=true on 404; want false")
	}
	if res.ID != "ag_x" {
		t.Errorf("ID=%q, want ag_x (echoed even on unreachable)", res.ID)
	}
}

func TestRunAgentStatus_NilConfig(t *testing.T) {
	// Defensive: Agent.Config is a pointer; nil should not panic
	svc := &fakeAgentStatusSvc{agent: &sdk.Agent{ID: "ag_x", Config: nil}}
	res, err := runAgentStatus(context.Background(), svc, "ag_x")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !res.Reachable {
		t.Error("Reachable=false, want true on nil config")
	}
	if res.ModelID != "" {
		t.Errorf("ModelID=%q, want empty (no config)", res.ModelID)
	}
}

func TestEmitAgentStatus_JSON(t *testing.T) {
	var buf bytes.Buffer
	res := &AgentStatusResult{ID: "ag_x", Reachable: true, ModelID: "m_x"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	if err := emitAgentStatus(res, fopts, &buf); err != nil {
		t.Fatalf("%v", err)
	}
	var env struct {
		OK   bool              `json:"ok"`
		Data AgentStatusResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("%v", err)
	}
	got := env.Data
	if got.ModelID != "m_x" {
		t.Errorf("ModelID=%q, want m_x", got.ModelID)
	}
}

func TestEmitAgentStatus_TextHuman(t *testing.T) {
	var buf bytes.Buffer
	res := &AgentStatusResult{ID: "ag_x", Reachable: true, ModelID: "m_x"}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}
	if err := emitAgentStatus(res, fopts, &buf); err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"ag_x", "m_x", "true"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	}
}
