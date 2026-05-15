package sessioncmd

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

// fakeViewService scripts a GetSession + LoadMessages response.
type fakeViewService struct {
	s        *sdk.Session
	err      error
	gotID    string
	msgs     []sdk.Message
	msgsErr  error
	loadCall struct {
		sessionID string
		limit     int
		called    bool
	}
}

func (f *fakeViewService) GetSession(_ context.Context, id string) (*sdk.Session, error) {
	f.gotID = id
	return f.s, f.err
}

func (f *fakeViewService) LoadMessages(_ context.Context, sessionID string, limit int, _ *time.Time) ([]sdk.Message, error) {
	f.loadCall.called = true
	f.loadCall.sessionID = sessionID
	f.loadCall.limit = limit
	return f.msgs, f.msgsErr
}

func TestView_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewService{s: &sdk.Session{
		ID:          "s_abc",
		Title:       "Design review",
		Description: "RAG chunking strategy review",
		CreatedAt:   "2026-05-10T09:00:00Z",
		UpdatedAt:   "2026-05-12T14:00:00Z",
	}}
	require.NoError(t, runView(context.Background(), &ViewOptions{}, nil, svc, "s_abc"))
	got := out.String()
	for _, want := range []string{"s_abc", "Design review", "RAG chunking strategy review", "2026-05-12"} {
		assert.Contains(t, got, want)
	}
	assert.Equal(t, "s_abc", svc.gotID)
}

func TestView_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewService{s: &sdk.Session{ID: "s_abc", Title: "T", UpdatedAt: "2026-05-12T14:00:00Z"}}
	require.NoError(t, runView(context.Background(), &ViewOptions{}, &cmdutil.JSONOptions{}, svc, "s_abc"))

	body := out.String()
	assert.True(t, strings.HasPrefix(strings.TrimSpace(body), `{"id":"s_abc"`), "bare object expected; got %q", body)
	assert.NotContains(t, body, `"ok":`)
}

func TestView_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeViewService{err: errors.New("HTTP error 404: not found")}
	err := runView(context.Background(), &ViewOptions{}, nil, svc, "s_missing")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestView_OmitsEmptyDescription(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewService{s: &sdk.Session{ID: "s_min", Title: "Bare"}}
	require.NoError(t, runView(context.Background(), &ViewOptions{}, nil, svc, "s_min"))
	// Empty Description should not produce an empty `DESC:` line.
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(line, "DESC:") {
			t.Errorf("empty description should be omitted, found %q", line)
		}
	}
}

// --- --full / --limit tests ---

func TestView_Full_LoadsMessages(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewService{
		s: &sdk.Session{ID: "s_abc", Title: "Chat"},
		msgs: []sdk.Message{
			{ID: "m1", Role: "user", Content: "What is RAG?", CreatedAt: time.Date(2026, 5, 15, 14, 32, 0, 0, time.UTC)},
			{ID: "m2", Role: "assistant", Content: "RAG stands for retrieval-augmented generation.", CreatedAt: time.Date(2026, 5, 15, 14, 32, 5, 0, time.UTC)},
		},
	}
	require.NoError(t, runView(context.Background(), &ViewOptions{Full: true, Limit: 50}, nil, svc, "s_abc"))
	got := out.String()
	assert.True(t, svc.loadCall.called, "expected LoadMessages to be called")
	assert.Equal(t, "s_abc", svc.loadCall.sessionID)
	assert.Equal(t, 50, svc.loadCall.limit)
	for _, want := range []string{"Messages (2)", "[user]", "[assistant]", "What is RAG?", "retrieval-augmented generation"} {
		assert.Contains(t, got, want)
	}
}

func TestView_Full_NoMessages(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewService{
		s:    &sdk.Session{ID: "s_empty", Title: "Empty"},
		msgs: []sdk.Message{},
	}
	require.NoError(t, runView(context.Background(), &ViewOptions{Full: true, Limit: 50}, nil, svc, "s_empty"))
	got := out.String()
	assert.Contains(t, got, "Messages (0)")
}

func TestView_Full_LimitInvalid_Zero(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeViewService{s: &sdk.Session{ID: "s"}}
	err := runView(context.Background(), &ViewOptions{Full: true, Limit: 0}, nil, svc, "s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.invalid_argument")
}

func TestView_Full_LimitInvalid_TooLarge(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeViewService{s: &sdk.Session{ID: "s"}}
	err := runView(context.Background(), &ViewOptions{Full: true, Limit: 1001}, nil, svc, "s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.invalid_argument")
}

// --limit without --full is rejected with input.invalid_argument — same
// pattern as `--title` requires `--from-url` in `doc upload`.
func TestView_LimitWithoutFull(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeViewService{s: &sdk.Session{ID: "s"}}
	err := runView(context.Background(), &ViewOptions{Full: false, Limit: 100, LimitSet: true}, nil, svc, "s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.invalid_argument")
	assert.Contains(t, err.Error(), "--limit")
	assert.Contains(t, err.Error(), "--full")
}

func TestView_Full_JSON_HasMessages(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewService{
		s: &sdk.Session{ID: "s_abc", Title: "T"},
		msgs: []sdk.Message{
			{ID: "m1", Role: "user", Content: "hi"},
		},
	}
	require.NoError(t, runView(context.Background(), &ViewOptions{Full: true, Limit: 50}, &cmdutil.JSONOptions{}, svc, "s_abc"))
	body := out.String()
	assert.Contains(t, body, `"messages":`)
	assert.Contains(t, body, `"id":"m1"`)
	assert.Contains(t, body, `"role":"user"`)
}

// Without --full, the LoadMessages SDK call must not fire and the JSON
// payload must not contain a `messages` key.
func TestView_NoFull_DoesNotCallLoadMessages(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeViewService{s: &sdk.Session{ID: "s_abc"}}
	require.NoError(t, runView(context.Background(), &ViewOptions{}, &cmdutil.JSONOptions{}, svc, "s_abc"))
	assert.False(t, svc.loadCall.called, "LoadMessages must not be called without --full")
	assert.NotContains(t, out.String(), `"messages":`)
}
