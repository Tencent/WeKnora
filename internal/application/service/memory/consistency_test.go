package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMemoryConsistencyCappedSessionsMustBeFollowedUp(t *testing.T) {
	s, tr, msg, model, q := newExtractionHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	model.response = accountResponse("一个会话", "用户在这个会话里说了两句话。")
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("session-%d", i)
		marker := fmt.Sprintf("unique-session-marker-%d", i)
		msg.set(id, settledConversation(id, marker, marker+"的后一句"))
		s.ScheduleExtraction(ctx, id, "m", "model")
	}
	drainExtractions(t, s, q)
	require.True(t, strings.Contains(model.seenTranscripts(), "unique-session-marker-3"), "fourth claimed session must survive the per-run call cap")
}
func TestMemoryConsistencyRetryMustKeepAllClaimedSessions(t *testing.T) {
	s, tr, msg, model, q := newExtractionHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	model.response = accountResponse("一个会话", "用户在这个会话里说了两句话。")
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("session-%d", i)
		marker := fmt.Sprintf("retry-session-marker-%d", i)
		msg.set(id, settledConversation(id, marker, marker+"的后一句"))
		s.ScheduleExtraction(ctx, id, "m", "model")
	}
	task := q.pop()
	require.NotNil(t, task)
	model.failNext = true
	require.Error(t, s.Handle(context.Background(), task))
	require.NoError(t, s.Handle(context.Background(), task))
	drainExtractions(t, s, q)
	require.True(t, strings.Contains(model.seenTranscripts(), "retry-session-marker-1"), "retry must retain the second drained session")
}

func TestMemoryConsistencyTimestampTiesAndLargePendingQueue(t *testing.T) {
	for _, manySessions := range []bool{false, true} {
		t.Run(fmt.Sprint(manySessions), func(t *testing.T) {
			s, tr, msg, model, q := newExtractionHarness(t)
			ctx := enabledCtx(t, tr, 1, "alice")
			model.response = accountResponse("很多话", "用户说了很多话。")
			at := time.Now().Add(-time.Hour)
			var rows []*types.Message
			for i := 0; i < 85; i++ {
				id := fmt.Sprintf("marker-%03d", i)
				session := "single"
				if manySessions {
					session = id
				}
				if manySessions {
					msg.set(session, []*types.Message{userMessage(session, id, at), userMessage(session, id+"-后一句", at.Add(time.Second))})
				} else {
					rows = append(rows, userMessage(session, id, at))
					msg.set(session, rows)
				}
				s.ScheduleExtraction(ctx, session, id, "model")
			}
			drainExtractions(t, s, q)
			var seen string
			for _, prompt := range model.promptsSeen() {
				seen += transcriptBlock(prompt)
			}
			for i := 0; i < 85; i++ {
				require.Contains(t, seen, fmt.Sprintf("marker-%03d", i), "every message must reach the model, whether the turns tie on time or pile up in one conversation")
			}
		})
	}
}

func TestMemoryConsistencyGapDoesNotSkipLaterTurns(t *testing.T) {
	s, tr, msg, model, q := newExtractionHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	model.response = accountResponse("断断续续的一天", "用户断断续续问了一整天。")
	at := time.Now().Add(-24 * time.Hour)
	var rows []*types.Message
	for i := 0; i < 7; i++ {
		rows = append(rows, userMessage("s", fmt.Sprintf("gap-marker-%d", i), at.Add(time.Duration(i)*2*time.Hour)))
	}
	msg.set("s", rows)
	s.ScheduleExtraction(ctx, "s", "m", "model")
	drainExtractions(t, s, q)
	var seen string
	for _, prompt := range model.promptsSeen() {
		seen += transcriptBlock(prompt)
	}
	for i := 0; i < 7; i++ {
		require.Contains(t, seen, fmt.Sprintf("gap-marker-%d", i), "a two-hour silence is part of the conversation, not the end of it")
	}
}

func TestMemoryConsistencyLeaseAndConcurrentEnqueue(t *testing.T) {
	s, _, tr := newMemoryHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := s.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	_, _, err = s.repo.EnqueuePendingSession(ctx, scope, "s", time.Minute)
	require.NoError(t, err)
	batch, err := s.repo.ClaimPendingSessions(ctx, scope, "s", "first", time.Minute)
	require.NoError(t, err)
	require.Len(t, batch.Sessions, 1)
	duplicate, err := s.repo.ClaimPendingSessions(ctx, scope, "s", "second", time.Minute)
	require.NoError(t, err)
	require.NotNil(t, duplicate)
	require.False(t, duplicate.RetryAt.IsZero())
	require.Empty(t, duplicate.Sessions)
	_, queued, err := s.repo.EnqueuePendingSession(ctx, scope, "s", time.Minute)
	require.NoError(t, err)
	require.False(t, queued)
	require.NoError(t, s.repo.CheckpointExtraction(ctx, scope, "first", batch.Sessions[0], types.MemoryMessageCursor{At: time.Now(), ID: "a"}, true))
	require.NoError(t, s.repo.FinishExtraction(ctx, scope, "first"))
	next, err := s.repo.ClaimPendingSessions(ctx, scope, "s", "next", time.Minute)
	require.NoError(t, err)
	require.Len(t, next.Sessions, 1, "a turn arriving while the batch runs must remain pending")
	require.NoError(t, s.repo.ReleaseExtractionSlot(ctx, scope, "first"))
	require.ErrorIs(t, s.repo.CheckpointExtraction(ctx, scope, "first", batch.Sessions[0], types.MemoryMessageCursor{}, true), types.ErrMemoryExtractionLeaseLost)
	require.NoError(t, s.repo.CheckpointExtraction(ctx, scope, "next", next.Sessions[0], next.Sessions[0].Cursor, true))
	require.NoError(t, s.repo.FinishExtraction(ctx, scope, "next"))
	empty, err := s.repo.ClaimPendingSessions(ctx, scope, "s", "duplicate", time.Minute)
	require.NoError(t, err)
	require.Nil(t, empty, "redelivering a finished task must be a no-op")
}
