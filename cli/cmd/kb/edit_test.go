package kb

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

// fakeEditSvc captures the (id, request) pair handed to UpdateKnowledgeBase.
type fakeEditSvc struct {
	gotID  string
	gotReq *sdk.UpdateKnowledgeBaseRequest
	resp   *sdk.KnowledgeBase
	err    error
}

func (f *fakeEditSvc) UpdateKnowledgeBase(_ context.Context, id string, req *sdk.UpdateKnowledgeBaseRequest) (*sdk.KnowledgeBase, error) {
	f.gotID = id
	f.gotReq = req
	return f.resp, f.err
}

func TestEdit_RequiresAtLeastOneFlag(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{}
	err := runEdit(context.Background(), &EditOptions{}, svc, "kb_abc")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code)
	assert.Contains(t, typed.Hint, "--name")
	assert.Contains(t, typed.Hint, "--description")
}

func TestEdit_OnlyName(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeEditSvc{resp: &sdk.KnowledgeBase{ID: "kb_abc", Name: "new"}}
	opts := &EditOptions{}
	opts.Name = stringPtr("new")
	require.NoError(t, runEdit(context.Background(), opts, svc, "kb_abc"))

	assert.Equal(t, "kb_abc", svc.gotID)
	require.NotNil(t, svc.gotReq)
	assert.Equal(t, "new", svc.gotReq.Name)
	// Description must be empty string (not "<nil>"), so server doesn't
	// confuse "unset" with "set-to-empty". Actually the SDK ships an empty
	// string either way — we just verify we didn't accidentally serialize a
	// description override.
	assert.Equal(t, "", svc.gotReq.Description)
	assert.Contains(t, out.String(), "kb_abc")
}

func TestEdit_OnlyDescription(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{resp: &sdk.KnowledgeBase{ID: "kb_abc"}}
	opts := &EditOptions{}
	opts.Description = stringPtr("new desc")
	require.NoError(t, runEdit(context.Background(), opts, svc, "kb_abc"))

	require.NotNil(t, svc.gotReq)
	assert.Equal(t, "new desc", svc.gotReq.Description)
	assert.Equal(t, "", svc.gotReq.Name)
}

func TestEdit_BothFlags(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{resp: &sdk.KnowledgeBase{ID: "kb_abc"}}
	opts := &EditOptions{}
	opts.Name = stringPtr("renamed")
	opts.Description = stringPtr("new desc")
	require.NoError(t, runEdit(context.Background(), opts, svc, "kb_abc"))
	assert.Equal(t, "renamed", svc.gotReq.Name)
	assert.Equal(t, "new desc", svc.gotReq.Description)
}

func TestEdit_DryRun_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	opts := &EditOptions{DryRun: true, JSONOut: true}
	opts.Name = stringPtr("preview")
	require.NoError(t, runEdit(context.Background(), opts, nil, "kb_abc"))

	body := out.String()
	assert.True(t, strings.HasPrefix(body, `{"ok":true`))
	assert.Contains(t, body, `"dry_run":true`)
	assert.Contains(t, body, `"write"`)
}

func TestEdit_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{err: errors.New("HTTP error 404: not found")}
	opts := &EditOptions{}
	opts.Name = stringPtr("x")
	err := runEdit(context.Background(), opts, svc, "kb_missing")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func stringPtr(s string) *string { return &s }
