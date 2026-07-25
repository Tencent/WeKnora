package cmdutil

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdk "github.com/Tencent/WeKnora/client"
)

type fakeVisibleKBLister struct {
	owned  []sdk.KnowledgeBase
	shared []sdk.SharedKnowledgeBaseInfo
}

func (f *fakeVisibleKBLister) ListKnowledgeBases(context.Context) ([]sdk.KnowledgeBase, error) {
	return f.owned, nil
}

func (f *fakeVisibleKBLister) ListSharedKnowledgeBases(context.Context) ([]sdk.SharedKnowledgeBaseInfo, error) {
	return f.shared, nil
}

func TestResolveKBNameToIDFindsSharedKnowledgeBase(t *testing.T) {
	sharedKB := sdk.KnowledgeBase{ID: "shared-id", Name: "Partner Docs"}
	id, err := ResolveKBNameToID(context.Background(), &fakeVisibleKBLister{
		shared: []sdk.SharedKnowledgeBaseInfo{{KnowledgeBase: &sharedKB}},
	}, "Partner Docs")
	require.NoError(t, err)
	assert.Equal(t, "shared-id", id)
}

func TestResolveKBNameToIDRejectsCrossTenantAmbiguity(t *testing.T) {
	sharedKB := sdk.KnowledgeBase{ID: "shared-id", Name: "Docs"}
	_, err := ResolveKBNameToID(context.Background(), &fakeVisibleKBLister{
		owned:  []sdk.KnowledgeBase{{ID: "owned-id", Name: "Docs"}},
		shared: []sdk.SharedKnowledgeBaseInfo{{KnowledgeBase: &sharedKB}},
	}, "Docs")
	require.Error(t, err)
	var typed *Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, CodeInputInvalidArgument, typed.Code)
}
