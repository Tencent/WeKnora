package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeQARejectsInvalidQueryBeforeSetup(t *testing.T) {
	svc := &sessionService{}

	err := svc.KnowledgeQA(context.Background(), &types.QARequest{
		Session: &types.Session{},
		Query:   "invalid\x00query",
	}, event.NewEventBus())

	require.EqualError(t, err, "user query contains invalid content")
}

func TestAgentQARejectsInvalidQueryBeforeSetup(t *testing.T) {
	svc := &sessionService{}

	err := svc.AgentQA(context.Background(), &types.QARequest{
		Session:     &types.Session{},
		Query:       "invalid\x00query",
		CustomAgent: &types.CustomAgent{},
	}, event.NewEventBus())

	require.EqualError(t, err, "user query contains invalid content")
}
