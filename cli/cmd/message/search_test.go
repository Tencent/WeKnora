package messagecmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeSearchSvc struct {
	result *sdk.MessageSearchResult
	err    error
	gotReq *sdk.SearchMessagesRequest
}

func (s *fakeSearchSvc) SearchMessages(_ context.Context, req *sdk.SearchMessagesRequest) (*sdk.MessageSearchResult, error) {
	s.gotReq = req
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

// --- baseline tests (from spec) ---

func TestRunSearch_EmitsItemsAndTotal(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSearchSvc{result: &sdk.MessageSearchResult{
		Items: []*sdk.MessageSearchGroupItem{{RequestID: "r1", SessionID: "s1", QueryContent: "q", AnswerContent: "a"}},
		Total: 7,
	}}
	opts := &SearchOptions{Query: "deploy steps", Limit: 20}
	require.NoError(t, runSearch(context.Background(), opts, jsonOpts(), svc))
	assert.Equal(t, "deploy steps", svc.gotReq.Query)
	assert.Contains(t, out.String(), `"request_id":"r1"`)
	assert.Contains(t, out.String(), `"total_count":7`)
}

func TestRunSearch_SessionScopePassedThrough(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSearchSvc{result: &sdk.MessageSearchResult{}}
	opts := &SearchOptions{Query: "q", Limit: 20, SessionIDs: []string{"s1", "s2"}}
	require.NoError(t, runSearch(context.Background(), opts, jsonOpts(), svc))
	assert.Equal(t, []string{"s1", "s2"}, svc.gotReq.SessionIDs)
}

// --- extended quality tests ---

// TestRunSearch_LimitOutOfRange_InvalidArgument asserts that a limit outside
// 1..1000 is rejected with a typed *cmdutil.Error and CodeInputInvalidArgument.
func TestRunSearch_LimitOutOfRange_InvalidArgument(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	cases := []struct {
		name  string
		limit int
	}{
		{"zero", 0},
		{"negative", -5},
		{"above max", 1001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runSearch(context.Background(), &SearchOptions{Query: "q", Limit: tc.limit}, jsonOpts(), &fakeSearchSvc{})
			require.Error(t, err)
			var cliErr *cmdutil.Error
			require.True(t, errors.As(err, &cliErr), "expected *cmdutil.Error, got %T", err)
			assert.Equal(t, cmdutil.CodeInputInvalidArgument, cliErr.Code)
		})
	}
}

// TestRunSearch_InvalidMode_InvalidArgument: a --mode outside the closed enum
// is rejected with input.invalid_argument rather than silently returning empty.
func TestRunSearch_InvalidMode_InvalidArgument(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSearchSvc{result: &sdk.MessageSearchResult{}}
	err := runSearch(context.Background(), &SearchOptions{Query: "q", Limit: 20, Mode: "hybird"}, jsonOpts(), svc)
	require.Error(t, err)
	var cliErr *cmdutil.Error
	require.True(t, errors.As(err, &cliErr), "expected *cmdutil.Error, got %T", err)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, cliErr.Code)
	assert.Nil(t, svc.gotReq, "must reject before calling the server")
}

// TestRunSearch_ModeNormalized: a valid mode in any case is normalized to the
// lowercase form the server matches before being sent.
func TestRunSearch_ModeNormalized(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSearchSvc{result: &sdk.MessageSearchResult{}}
	require.NoError(t, runSearch(context.Background(), &SearchOptions{Query: "q", Limit: 20, Mode: "Hybrid"}, jsonOpts(), svc))
	assert.Equal(t, "hybrid", svc.gotReq.Mode)
}

// TestRunSearch_TextMode_NewlineInContent asserts that embedded newlines in
// query/answer content are collapsed to a single tabwriter row (OneLine).
func TestRunSearch_TextMode_NewlineInContent(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSearchSvc{result: &sdk.MessageSearchResult{
		Items: []*sdk.MessageSearchGroupItem{{
			SessionID:     "sess1",
			QueryContent:  "line one\nline two\nline three",
			AnswerContent: "answer line\nanother line",
			Score:         0.9,
		}},
		Total: 1,
	}}
	opts := &SearchOptions{Query: "q", Limit: 20}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatText}
	require.NoError(t, runSearch(context.Background(), opts, fopts, svc))
	got := out.String()
	// Header + 1 data row = exactly 2 lines.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	assert.Len(t, lines, 2, "newlines in content must be collapsed to a single row: got %q", got)
	assert.Contains(t, lines[1], "sess1")
	// Score must render with the %.2f format: 0.9 → "0.90" (pin the formatting).
	assert.Contains(t, lines[1], "0.90", "score must render with %%.2f formatting: got %q", got)
}

// TestRunSearch_ServiceError_ReturnsError asserts that a service-level error
// propagates out of runSearch.
func TestRunSearch_ServiceError_ReturnsError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSearchSvc{err: errors.New("HTTP error 503: service unavailable")}
	err := runSearch(context.Background(), &SearchOptions{Query: "q", Limit: 20}, jsonOpts(), svc)
	require.Error(t, err)
}

// TestRunSearch_EmptyResult_JSONArrayNotNull asserts that an empty result set
// is serialised as "data":[] and not "data":null.
func TestRunSearch_EmptyResult_JSONArrayNotNull(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSearchSvc{result: &sdk.MessageSearchResult{Items: nil, Total: 0}}
	opts := &SearchOptions{Query: "q", Limit: 20}
	require.NoError(t, runSearch(context.Background(), opts, jsonOpts(), svc))
	assert.Contains(t, out.String(), `"data":[]`)
}

// TestRunSearch_ModePassedThrough asserts that a valid --mode value is
// forwarded to the SDK request.
func TestRunSearch_ModePassedThrough(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSearchSvc{result: &sdk.MessageSearchResult{}}
	opts := &SearchOptions{Query: "q", Limit: 20, Mode: "vector"}
	require.NoError(t, runSearch(context.Background(), opts, jsonOpts(), svc))
	assert.Equal(t, "vector", svc.gotReq.Mode)
}
