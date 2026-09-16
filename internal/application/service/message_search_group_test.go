package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestGroupByRequestIDKeepsSessionsApart(t *testing.T) {
	items := []*types.MessageSearchResultItem{
		{MessageWithSession: types.MessageWithSession{Message: types.Message{
			ID: "p-u", SessionID: "parent", RequestID: "req", Role: "user", Content: "q-parent",
		}}},
		{MessageWithSession: types.MessageWithSession{Message: types.Message{
			ID: "f-u", SessionID: "fork", RequestID: "req", Role: "user", Content: "q-fork",
		}}},
	}

	got := groupByRequestID(items)

	require.Len(t, got, 2)
	bySession := map[string]string{}
	for _, g := range got {
		bySession[g.SessionID] = g.QueryContent
	}
	require.Equal(t, "q-parent", bySession["parent"])
	require.Equal(t, "q-fork", bySession["fork"])
}

type partnerLookupRepo struct {
	interfaces.MessageRepository
	calls []partnerLookupCall
}

type partnerLookupCall struct {
	sessionID  string
	requestIDs []string
}

func (r *partnerLookupRepo) GetMessagesByRequestIDs(
	_ context.Context, sessionID string, requestIDs []string,
) ([]*types.MessageWithSession, error) {
	copied := append([]string(nil), requestIDs...)
	r.calls = append(r.calls, partnerLookupCall{sessionID: sessionID, requestIDs: copied})
	return nil, nil
}

func TestFetchPartnerMessagesQueriesEachSessionSeparately(t *testing.T) {
	repo := &partnerLookupRepo{}
	svc := &messageService{messageRepo: repo}
	items := []*types.MessageSearchResultItem{
		{MessageWithSession: types.MessageWithSession{Message: types.Message{
			ID: "p-u", SessionID: "parent", RequestID: "req", Role: "user",
		}}},
		{MessageWithSession: types.MessageWithSession{Message: types.Message{
			ID: "f-u", SessionID: "fork", RequestID: "req", Role: "user",
		}}},
	}

	got := svc.fetchPartnerMessages(context.Background(), items)

	require.Equal(t, items, got)
	require.Len(t, repo.calls, 2)
	bySession := map[string][]string{}
	for _, call := range repo.calls {
		bySession[call.sessionID] = call.requestIDs
	}
	require.Equal(t, []string{"req"}, bySession["parent"])
	require.Equal(t, []string{"req"}, bySession["fork"])
}
