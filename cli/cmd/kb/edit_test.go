package kb

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeEditSvc captures the (id, request) pair handed to UpdateKnowledgeBase
// and scripts the GetKnowledgeBase fetch used by the fetch-then-update path.
type fakeEditSvc struct {
	current    *sdk.KnowledgeBase // returned by GetKnowledgeBase
	currentErr error
	gotID      string
	gotReq     *sdk.UpdateKnowledgeBaseRequest
	resp       *sdk.KnowledgeBase
	err        error
}

func (f *fakeEditSvc) GetKnowledgeBase(_ context.Context, id string) (*sdk.KnowledgeBase, error) {
	if f.currentErr != nil {
		return nil, f.currentErr
	}
	if f.current != nil {
		return f.current, nil
	}
	return &sdk.KnowledgeBase{ID: id}, nil
}

func (f *fakeEditSvc) UpdateKnowledgeBase(_ context.Context, id string, req *sdk.UpdateKnowledgeBaseRequest) (*sdk.KnowledgeBase, error) {
	f.gotID = id
	f.gotReq = req
	return f.resp, f.err
}

func TestEdit_RequiresAtLeastOneFlag(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{}
	err := runEdit(context.Background(), &EditOptions{}, nil, svc, "kb_abc")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code)
	assert.Contains(t, typed.Hint, "--name")
	assert.Contains(t, typed.Hint, "--description")
}

// When only --name is passed, the request must carry the user's new name
// AND the current Description (preserved via the fetch). Sending Description=""
// would clobber the server-side value because UpdateKnowledgeBaseRequest
// fields are `string`, not `*string`.
func TestEdit_OnlyName_PreservesCurrentDescription(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		current: &sdk.KnowledgeBase{ID: "kb_abc", Name: "old", Description: "keep me"},
		resp:    &sdk.KnowledgeBase{ID: "kb_abc", Name: "new", Description: "keep me"},
	}
	opts := &EditOptions{}
	opts.Name = stringPtr("new")
	require.NoError(t, runEdit(context.Background(), opts, nil, svc, "kb_abc"))

	assert.Equal(t, "kb_abc", svc.gotID)
	require.NotNil(t, svc.gotReq)
	assert.Equal(t, "new", svc.gotReq.Name)
	assert.Equal(t, "keep me", svc.gotReq.Description, "Description must be preserved from fetch when not in --description")
	assert.Contains(t, out.String(), "kb_abc")
}

// Symmetric: only --description must preserve current Name. The server's
// `Name required` validation made this case fail without the fetch.
func TestEdit_OnlyDescription_PreservesCurrentName(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{
		current: &sdk.KnowledgeBase{ID: "kb_abc", Name: "keep me", Description: "old"},
		resp:    &sdk.KnowledgeBase{ID: "kb_abc"},
	}
	opts := &EditOptions{}
	opts.Description = stringPtr("new desc")
	require.NoError(t, runEdit(context.Background(), opts, nil, svc, "kb_abc"))

	require.NotNil(t, svc.gotReq)
	assert.Equal(t, "new desc", svc.gotReq.Description)
	assert.Equal(t, "keep me", svc.gotReq.Name, "Name must be preserved from fetch when not in --name")
}

func TestEdit_BothFlags(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeEditSvc{resp: &sdk.KnowledgeBase{ID: "kb_abc"}}
	opts := &EditOptions{}
	opts.Name = stringPtr("renamed")
	opts.Description = stringPtr("new desc")
	require.NoError(t, runEdit(context.Background(), opts, nil, svc, "kb_abc"))
	assert.Equal(t, "renamed", svc.gotReq.Name)
	assert.Equal(t, "new desc", svc.gotReq.Description)
}

func TestEdit_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	// 404 must come from the GetKnowledgeBase pre-fetch in the fetch-then-
	// update flow - that's the first server roundtrip when the id is bad.
	svc := &fakeEditSvc{currentErr: errors.New("HTTP error 404: not found")}
	opts := &EditOptions{}
	opts.Name = stringPtr("x")
	err := runEdit(context.Background(), opts, nil, svc, "kb_missing")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func stringPtr(s string) *string { return &s }
