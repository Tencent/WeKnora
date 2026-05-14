package sessioncmd

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

// fakeViewService scripts a GetSession response.
type fakeViewService struct {
	s     *sdk.Session
	err   error
	gotID string
}

func (f *fakeViewService) GetSession(_ context.Context, id string) (*sdk.Session, error) {
	f.gotID = id
	return f.s, f.err
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
