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

// fakePinSvc satisfies PinService: GetKnowledgeBase + TogglePinKnowledgeBase.
type fakePinSvc struct {
	current      sdk.KnowledgeBase
	getErr       error
	toggleErr    error
	toggleCalled bool
}

func (f *fakePinSvc) GetKnowledgeBase(_ context.Context, id string) (*sdk.KnowledgeBase, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	c := f.current
	c.ID = id
	return &c, nil
}

func (f *fakePinSvc) TogglePinKnowledgeBase(_ context.Context, id string) (*sdk.KnowledgeBase, error) {
	f.toggleCalled = true
	if f.toggleErr != nil {
		return nil, f.toggleErr
	}
	c := f.current
	c.ID = id
	c.IsPinned = !c.IsPinned
	return &c, nil
}

func TestPin_UnpinnedToPinned_CallsToggle(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakePinSvc{current: sdk.KnowledgeBase{IsPinned: false}}
	require.NoError(t, runPin(context.Background(), &PinOptions{}, nil, svc, "kb_abc", true))
	assert.True(t, svc.toggleCalled, "must call toggle when current state differs")
	assert.Contains(t, out.String(), "kb_abc")
}

func TestPin_AlreadyPinned_NoOp(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakePinSvc{current: sdk.KnowledgeBase{IsPinned: true}}
	require.NoError(t, runPin(context.Background(), &PinOptions{}, nil, svc, "kb_abc", true))
	assert.False(t, svc.toggleCalled, "already pinned ⇒ must not call toggle")
	assert.Contains(t, out.String(), "already pinned")
}

func TestUnpin_PinnedToUnpinned_CallsToggle(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakePinSvc{current: sdk.KnowledgeBase{IsPinned: true}}
	require.NoError(t, runPin(context.Background(), &PinOptions{}, nil, svc, "kb_abc", false))
	assert.True(t, svc.toggleCalled)
}

func TestUnpin_AlreadyUnpinned_NoOp(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakePinSvc{current: sdk.KnowledgeBase{IsPinned: false}}
	require.NoError(t, runPin(context.Background(), &PinOptions{}, nil, svc, "kb_abc", false))
	assert.False(t, svc.toggleCalled, "already unpinned ⇒ must not call toggle")
	assert.Contains(t, out.String(), "already unpinned")
}

func TestPin_NotFound(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakePinSvc{getErr: errors.New("HTTP error 404: not found")}
	err := runPin(context.Background(), &PinOptions{}, nil, svc, "kb_missing", true)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
	assert.False(t, svc.toggleCalled)
}

func TestPin_ToggleError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakePinSvc{
		current:   sdk.KnowledgeBase{IsPinned: false},
		toggleErr: errors.New("HTTP error 500: internal"),
	}
	err := runPin(context.Background(), &PinOptions{}, nil, svc, "kb_abc", true)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestPin_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakePinSvc{current: sdk.KnowledgeBase{IsPinned: false}}
	require.NoError(t, runPin(context.Background(), &PinOptions{}, &cmdutil.JSONOptions{}, svc, "kb_abc", true))
	body := out.String()
	assert.Contains(t, body, `"is_pinned":true`)
	assert.Contains(t, body, `"id":"kb_abc"`)
}
