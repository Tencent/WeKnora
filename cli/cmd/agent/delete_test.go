package agentcmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

type fakeDeleteSvc struct {
	gotID string
	err   error
}

func (f *fakeDeleteSvc) DeleteAgent(_ context.Context, id string) error {
	f.gotID = id
	return f.err
}

func TestDelete_NonTTY_NoYes_ExitTen(t *testing.T) {
	_, _ = iostreams.SetForTest(t) // non-TTY
	svc := &fakeDeleteSvc{}
	err := runDelete(
		context.Background(),
		&DeleteOptions{AgentID: "ag_abc", Yes: false},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, &testutil.ConfirmPrompter{},
	)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputConfirmationRequired, typed.Code)
	assert.Empty(t, svc.gotID, "must not call DeleteAgent without confirm")
	assert.Equal(t, 10, cmdutil.ExitCode(err), "exit code 10 per destructive-write protocol")
}

func TestDelete_NonTTY_WithYes_Direct(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	require.NoError(t, runDelete(
		context.Background(),
		&DeleteOptions{AgentID: "ag_abc", Yes: true},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{},
	))
	assert.Equal(t, "ag_abc", svc.gotID)
	assert.Contains(t, out.String(), "ag_abc")
}

func TestDelete_404_PropagatesNotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{err: errBadHTTP404}
	err := runDelete(
		context.Background(),
		&DeleteOptions{AgentID: "ag_missing", Yes: true},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, &testutil.ConfirmPrompter{},
	)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestDelete_TTY_ConfirmYes(t *testing.T) {
	_, _ = iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: true}
	require.NoError(t, runDelete(
		context.Background(),
		&DeleteOptions{AgentID: "ag_abc"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, p,
	))
	assert.True(t, p.Asked)
	assert.Equal(t, "ag_abc", svc.gotID)
}

func TestDelete_TTY_ConfirmNo(t *testing.T) {
	_, errBuf := iostreams.SetForTestWithTTY(t)
	svc := &fakeDeleteSvc{}
	p := &testutil.ConfirmPrompter{Answer: false}
	err := runDelete(
		context.Background(),
		&DeleteOptions{AgentID: "ag_abc"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, p,
	)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeUserAborted, typed.Code)
	assert.Empty(t, svc.gotID, "answer=no must not call DeleteAgent")
	assert.Contains(t, errBuf.String(), "Aborted")
}

func TestDelete_JSON_BareObject(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDeleteSvc{}
	require.NoError(t, runDelete(
		context.Background(),
		&DeleteOptions{AgentID: "ag_abc", Yes: true},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, &testutil.ConfirmPrompter{},
	))
	assert.Contains(t, out.String(), `"id":"ag_abc"`)
	assert.Contains(t, out.String(), `"deleted":true`)
}
