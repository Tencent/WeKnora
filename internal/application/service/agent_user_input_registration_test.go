package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/agent/userinput"
	"github.com/Tencent/WeKnora/internal/types"
)

type registrationUserInputRequester struct{}

func (registrationUserInputRequester) RequestAndWait(context.Context, userinput.PendingRequest) (userinput.Result, error) {
	return userinput.Result{Status: userinput.StatusSkipped}, nil
}

func TestUserInputEnabledOnlyForWebChannel(t *testing.T) {
	tests := []struct {
		channel string
		want    bool
	}{
		{channel: "web", want: true},
		{channel: "WEB", want: true},
		{channel: "api", want: false},
		{channel: "im", want: false},
		{channel: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			if got := interactiveUserInputEnabled(tt.channel); got != tt.want {
				t.Fatalf("interactiveUserInputEnabled(%q) = %v, want %v", tt.channel, got, tt.want)
			}
		})
	}
}

func TestRegisterUserInputTool(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		requester userinput.Requester
		want      bool
	}{
		{name: "enabled", enabled: true, requester: registrationUserInputRequester{}, want: true},
		{name: "disabled channel", enabled: false, requester: registrationUserInputRequester{}, want: false},
		{name: "missing gate", enabled: true, requester: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := tools.NewToolRegistry()
			svc := &agentService{userInputRequester: tt.requester}
			svc.registerUserInputTool(registry, &types.AgentConfig{InteractiveUserInputEnabled: tt.enabled})
			_, err := registry.GetTool(tools.ToolAskUser)
			if got := err == nil; got != tt.want {
				t.Fatalf("ask_user registered = %v, want %v", got, tt.want)
			}
		})
	}
}
