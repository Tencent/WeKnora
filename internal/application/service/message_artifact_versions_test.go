package service

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type versionMessageRepo struct {
	interfaces.MessageRepository
	artifacts []types.MessageArtifact
	calls     int
}

func (r *versionMessageRepo) GetSessionArtifacts(context.Context, string) (types.MessageArtifacts, error) {
	r.calls++
	return r.artifacts, nil
}

func TestHistoricalVersionClarificationAcrossPagination(t *testing.T) {
	old := types.MessageArtifact{URL: "resource://dHZ_fFslfs0GgJGaJZGjGA", FileName: "deck.pptx", SourcePath: "/workspace/output/deck.pptx"}
	next := old
	next.URL = "resource://4N1nAo-FZZoDEExDQz2yoA"
	repo := &versionMessageRepo{artifacts: []types.MessageArtifact{old, next}}
	s := &messageService{messageRepo: repo}
	body := "![deck](" + old.URL + ")"
	message := &types.Message{Content: body, Artifacts: types.MessageArtifacts{next}}
	got := s.clarifyReadArtifactVersions(context.Background(), "session", []*types.Message{message})
	require.Contains(t, got[0].Content, next.URL)
	require.Equal(t, body, message.Content, "read repair must not mutate stored history")
	require.Equal(t, 1, repo.calls)
	first := &types.Message{Content: body, Artifacts: types.MessageArtifacts{old}}
	require.Equal(t, body, s.clarifyReadArtifactVersions(context.Background(), "session", []*types.Message{first})[0].Content)
	require.Equal(t, 1, repo.calls, "matching current references need no session-wide lookup")
}

func TestSessionArtifactsReadsEveryRecordedArtifact(t *testing.T) {
	old := types.MessageArtifact{URL: "resource://dHZ_fFslfs0GgJGaJZGjGA", FileName: "deck.pptx"}
	c := &ArtifactCollector{store: &fakeStore{prev: []types.MessageArtifact{old}}}
	require.Equal(t, types.MessageArtifacts{old}, c.SessionArtifacts(context.Background(), "session"))

	// A missing store, an empty session, and a lookup failure all degrade to
	// "nothing to resolve" rather than failing the turn.
	require.Nil(t, (&ArtifactCollector{}).SessionArtifacts(context.Background(), "session"))
	require.Nil(t, c.SessionArtifacts(context.Background(), ""))
	require.Nil(t, (&ArtifactCollector{
		store: &fakeStore{err: stderrors.New("boom")},
	}).SessionArtifacts(context.Background(), "session"))
}
