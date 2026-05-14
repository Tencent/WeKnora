package sessioncmd

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

// fakeDeleteSvc records what id was deleted.
type fakeDeleteSvc struct {
	err    error
	gotID  string
	called bool
}

func (f *fakeDeleteSvc) DeleteSession(_ context.Context, id string) error {
	f.called = true
	f.gotID = id
	return f.err
}

func TestDelete_WithYes(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{}
	require.NoError(t, runDelete(context.Background(), &DeleteOptions{Yes: true}, nil, svc, p, "s_abc"))
	assert.True(t, svc.called)
	assert.Equal(t, "s_abc", svc.gotID)
	assert.False(t, p.Asked, "-y must skip prompt")
	assert.Contains(t, out.String(), "Deleted")
}

func TestDelete_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errors.New("HTTP error 404: not found")}
	err := runDelete(context.Background(), &DeleteOptions{Yes: true}, nil, svc, &testutil.ConfirmPrompter{}, "s_missing")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestDelete_NonTTY_NoYes_RequiresConfirmation(t *testing.T) {
	iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	err := runDelete(context.Background(), &DeleteOptions{}, nil, svc, &testutil.ConfirmPrompter{}, "s_x")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Equal(t, 10, cmdutil.ExitCode(err))
	assert.False(t, svc.called, "non-TTY without -y must not call DeleteSession")
}

func TestDelete_TTY_ConfirmYes(t *testing.T) {
	_, _ = iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: true}
	require.NoError(t, runDelete(context.Background(), &DeleteOptions{}, nil, svc, p, "s_yes"))
	assert.True(t, p.Asked)
	assert.True(t, svc.called)
}

func TestDelete_TTY_ConfirmNo(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: false}
	err := runDelete(context.Background(), &DeleteOptions{}, nil, svc, p, "s_no")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.False(t, svc.called)
	assert.Contains(t, errBuf.String(), "Aborted")
}

