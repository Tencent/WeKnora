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

// fakeCreateSvc captures the request and returns canned responses.
type fakeCreateSvc struct {
	resp *sdk.KnowledgeBase
	err  error
	got  *sdk.KnowledgeBase
}

func (f *fakeCreateSvc) CreateKnowledgeBase(_ context.Context, kb *sdk.KnowledgeBase) (*sdk.KnowledgeBase, error) {
	f.got = kb
	return f.resp, f.err
}

func TestCreate_Success_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeCreateSvc{resp: &sdk.KnowledgeBase{
		ID:               "kb_new",
		Name:             "Marketing",
		Description:      "team docs",
		EmbeddingModelID: "model_x",
	}}
	opts := &CreateOptions{
		Name:           "Marketing",
		Description:    "team docs",
		EmbeddingModel: "model_x",
	}
	require.NoError(t, runCreate(context.Background(), opts, nil, svc))

	// Body sent to SDK matches flags.
	require.NotNil(t, svc.got)
	assert.Equal(t, "Marketing", svc.got.Name)
	assert.Equal(t, "team docs", svc.got.Description)
	assert.Equal(t, "model_x", svc.got.EmbeddingModelID)

	got := out.String()
	for _, want := range []string{"✓", "Created", "Marketing", "kb_new"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q in:\n%s", want, got)
		}
	}
}

func TestCreate_Success_OmitsEmbeddingModelWhenEmpty(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{resp: &sdk.KnowledgeBase{ID: "kb_x", Name: "n"}}
	opts := &CreateOptions{Name: "n"}
	require.NoError(t, runCreate(context.Background(), opts, nil, svc))

	require.NotNil(t, svc.got)
	assert.Equal(t, "", svc.got.EmbeddingModelID, "embedding-model unset ⇒ empty in request")
}

func TestCreate_NameRequired(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{}
	err := runCreate(context.Background(), &CreateOptions{}, nil, svc)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	assert.Nil(t, svc.got, "service must not be called when name is missing")
}

func TestCreate_NameWhitespaceOnly(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{}
	err := runCreate(context.Background(), &CreateOptions{Name: "   "}, nil, svc)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestCreate_HTTPError_500(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{err: errors.New("HTTP error 500: internal")}
	err := runCreate(context.Background(), &CreateOptions{Name: "x"}, nil, svc)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestCreate_HTTPError_409Conflict(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeCreateSvc{err: errors.New("HTTP error 409: name exists")}
	err := runCreate(context.Background(), &CreateOptions{Name: "dup"}, nil, svc)
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceAlreadyExists, typed.Code)
}

func TestCreate_JSONOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeCreateSvc{resp: &sdk.KnowledgeBase{ID: "kb_99", Name: "Eng"}}
	opts := &CreateOptions{Name: "Eng"}
	require.NoError(t, runCreate(context.Background(), opts, &cmdutil.JSONOptions{}, svc))

	got := out.String()
	assert.True(t, strings.HasPrefix(strings.TrimSpace(got), `{"id":"kb_99"`), "expected bare KnowledgeBase object; got %q", got)
	assert.Contains(t, got, `"name":"Eng"`)
	assert.NotContains(t, got, `"ok":`)
}
