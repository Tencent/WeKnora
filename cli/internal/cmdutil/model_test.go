package cmdutil

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdk "github.com/Tencent/WeKnora/client"
)

type fakeModelLister struct {
	models []sdk.Model
	err    error
}

func (f *fakeModelLister) ListModels(_ context.Context) ([]sdk.Model, error) {
	return f.models, f.err
}

func TestResolveModelRef(t *testing.T) {
	lister := &fakeModelLister{models: []sdk.Model{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "qwen2", Type: sdk.ModelTypeKnowledgeQA},
		{ID: "22222222-2222-2222-2222-222222222222", Name: "nomic", Type: sdk.ModelTypeEmbedding},
		{ID: "33333333-3333-3333-3333-333333333333", Name: "dup", Type: sdk.ModelTypeEmbedding},
		{ID: "44444444-4444-4444-4444-444444444444", Name: "dup", Type: sdk.ModelTypeKnowledgeQA},
	}}

	// A UUID passes through untouched without hitting the lister.
	got, err := ResolveModelRef(context.Background(), &fakeModelLister{err: errors.New("must not call")},
		"99999999-9999-9999-9999-999999999999", "Embedding")
	require.NoError(t, err)
	assert.Equal(t, "99999999-9999-9999-9999-999999999999", got)

	// Empty passes through (caller treats as unset).
	got, err = ResolveModelRef(context.Background(), lister, "", "")
	require.NoError(t, err)
	assert.Equal(t, "", got)

	// Name + type → id.
	got, err = ResolveModelRef(context.Background(), lister, "qwen2", "KnowledgeQA")
	require.NoError(t, err)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", got)

	// Type filter disambiguates same-named models across types.
	got, err = ResolveModelRef(context.Background(), lister, "dup", "Embedding")
	require.NoError(t, err)
	assert.Equal(t, "33333333-3333-3333-3333-333333333333", got)

	// No match → resource.not_found.
	_, err = ResolveModelRef(context.Background(), lister, "missing", "Embedding")
	var ce *Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CodeResourceNotFound, ce.Code)

	// Ambiguous (same name, no type filter) → input.invalid_argument.
	_, err = ResolveModelRef(context.Background(), lister, "dup", "")
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CodeInputInvalidArgument, ce.Code)
}
