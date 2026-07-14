package messagecmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// jsonOpts is the package-local FormatOptions shorthand (same pattern as
// chunk delete_test.go's inline construction).
func jsonOpts() *cmdutil.FormatOptions { return &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON} }

type fakeListSvc struct {
	items     []sdk.Message
	err       error
	gotSessID string
	gotLimit  int
	gotBefore *time.Time
}

func (s *fakeListSvc) LoadMessages(_ context.Context, sessionID string, limit int, before *time.Time) ([]sdk.Message, error) {
	s.gotSessID, s.gotLimit, s.gotBefore = sessionID, limit, before
	return s.items, s.err
}

func TestRunList_PassesArgsAndEmitsJSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{items: []sdk.Message{{ID: "m1", SessionID: "s1", Role: "assistant", Content: "hi"}}}
	opts := &ListOptions{SessionID: "s1", Limit: 20}
	require.NoError(t, runList(context.Background(), opts, jsonOpts(), svc))
	assert.Equal(t, "s1", svc.gotSessID)
	assert.Equal(t, 20, svc.gotLimit)
	assert.Nil(t, svc.gotBefore)
	assert.Contains(t, out.String(), `"id":"m1"`)
	assert.Contains(t, out.String(), `"count":1`)
	// message list never emits has_more: the server provides no total or
	// cursor, so the CLI does not fabricate one (omitempty keeps it absent)
	assert.NotContains(t, out.String(), `"has_more"`)
}

func TestRunList_BeforeParsedAsRFC3339(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeListSvc{}
	opts := &ListOptions{SessionID: "s1", Limit: 20, Before: "2026-06-01T00:00:00Z"}
	require.NoError(t, runList(context.Background(), opts, jsonOpts(), svc))
	require.NotNil(t, svc.gotBefore)
	assert.Equal(t, 2026, svc.gotBefore.Year())
}

func TestRunList_BadBeforeIsInvalidArgument(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runList(context.Background(), &ListOptions{SessionID: "s1", Limit: 20, Before: "yesterday"}, jsonOpts(), &fakeListSvc{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RFC3339")
	var cliErr *cmdutil.Error
	require.True(t, errors.As(err, &cliErr))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, cliErr.Code)
}

func TestRunList_EmptyIsJSONArrayNotNull(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	require.NoError(t, runList(context.Background(), &ListOptions{SessionID: "s1", Limit: 20}, jsonOpts(), &fakeListSvc{}))
	assert.Contains(t, out.String(), `"data":[]`)
}

// TestRunList_TextMode_NewlineInContent asserts that a message whose Content
// contains embedded newlines is collapsed to a single tabwriter row (OneLine).
func TestRunList_TextMode_NewlineInContent(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	ts := time.Now().Add(-5 * time.Minute)
	svc := &fakeListSvc{items: []sdk.Message{
		{ID: "m1", Role: "user", Content: "first line\nsecond line\nthird line", CreatedAt: ts},
	}}
	opts := &ListOptions{SessionID: "s1", Limit: 20}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
	require.NoError(t, runList(context.Background(), opts, fopts, svc))
	got := out.String()
	// Header + 1 data row = exactly 2 lines. TrimRight removes the trailing
	// newline that tabwriter emits, so Split should produce exactly 2 tokens.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	assert.Len(t, lines, 2, "newlines in content must be collapsed to a single row: got %q", got)
	// The data row must contain "m1" (id) and not a bare literal \n inside it.
	assert.Contains(t, lines[1], "m1")
}

// TestRunList_ServiceError_ReturnsError asserts that a service-level error
// propagates out of runList.
func TestRunList_ServiceError_ReturnsError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeListSvc{err: errors.New("HTTP error 503: service unavailable")}
	err := runList(context.Background(), &ListOptions{SessionID: "s1", Limit: 20}, jsonOpts(), svc)
	require.Error(t, err)
}

// TestRunList_NoHasMoreEvenWhenLimitFilled pins the contract that message
// list never fabricates has_more — even a limit-filled batch (the case a
// heuristic would flag) must not emit the key, because the server provides
// no total/cursor and elsewhere has_more means client-side truncation.
func TestRunList_NoHasMoreEvenWhenLimitFilled(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	items := []sdk.Message{
		{ID: "m1", Role: "user", Content: "a"},
		{ID: "m2", Role: "assistant", Content: "b"},
		{ID: "m3", Role: "user", Content: "c"},
	}
	opts := &ListOptions{SessionID: "s1", Limit: 3}
	svc := &fakeListSvc{items: items}
	require.NoError(t, runList(context.Background(), opts, jsonOpts(), svc))
	assert.NotContains(t, out.String(), `"has_more"`)
}
