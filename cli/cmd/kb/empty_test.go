package kb

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeEmptySvc records calls + scripts the response.
type fakeEmptySvc struct {
	err    error
	gotID  string
	called bool
	resp   *sdk.ClearKnowledgeBaseContentsResponse
}

func (f *fakeEmptySvc) ClearKnowledgeBaseContents(_ context.Context, id string) (*sdk.ClearKnowledgeBaseContentsResponse, error) {
	f.called = true
	f.gotID = id
	if f.err != nil {
		return nil, f.err
	}
	if f.resp == nil {
		return &sdk.ClearKnowledgeBaseContentsResponse{DeletedCount: 0}, nil
	}
	return f.resp, nil
}

func TestEmpty_WithYes(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeEmptySvc{resp: &sdk.ClearKnowledgeBaseContentsResponse{DeletedCount: 42}}
	require.NoError(t, runEmpty(context.Background(), &EmptyOptions{Yes: true}, nil, svc, &testutil.ConfirmPrompter{}, "kb_abc"))
	assert.True(t, svc.called)
	assert.Equal(t, "kb_abc", svc.gotID)
	body := out.String()
	assert.Contains(t, body, "kb_abc")
	assert.Contains(t, body, "42")
}

func TestEmpty_NonTTY_NoYes_RequiresConfirmation(t *testing.T) {
	iostreams.SetForTest(t)
	svc := &fakeEmptySvc{}
	err := runEmpty(context.Background(), &EmptyOptions{}, nil, svc, &testutil.ConfirmPrompter{}, "kb_abc")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err))
	assert.False(t, svc.called)
}

func TestEmpty_TTY_ConfirmNo(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeEmptySvc{}
	p := &testutil.ConfirmPrompter{Answer: false}
	err := runEmpty(context.Background(), &EmptyOptions{}, nil, svc, p, "kb_abc")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.False(t, svc.called)
	assert.Contains(t, errBuf.String(), "Aborted")
}

func TestEmpty_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEmptySvc{err: errors.New("HTTP error 404: not found")}
	err := runEmpty(context.Background(), &EmptyOptions{Yes: true}, nil, svc, &testutil.ConfirmPrompter{}, "kb_missing")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

