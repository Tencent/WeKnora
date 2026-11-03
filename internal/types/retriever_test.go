package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetrieveParamsValidateScope(t *testing.T) {
	tests := []struct {
		name    string
		params  RetrieveParams
		wantErr bool
	}{
		{
			name:   "knowledge base scope only",
			params: RetrieveParams{KnowledgeBaseIDs: []string{"kb-1"}},
		},
		{
			name:   "knowledge scope only",
			params: RetrieveParams{KnowledgeIDs: []string{"knowledge-1"}},
		},
		{
			name: "both scopes",
			params: RetrieveParams{
				KnowledgeBaseIDs: []string{"kb-1"},
				KnowledgeIDs:     []string{"knowledge-1"},
			},
		},
		{
			name:    "empty scope",
			params:  RetrieveParams{Query: "text", TopK: 5, RetrieverType: VectorRetrieverType},
			wantErr: true,
		},
		{
			name:    "tag scope is not a retrieval scope",
			params:  RetrieveParams{TagIDs: []string{"tag-1"}},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.params.ValidateScope()
			if !testCase.wantErr {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrRetrieveScopeRequired)
		})
	}
}

func TestErrRetrieveScopeRequiredIsStable(t *testing.T) {
	// Callers match on errors.Is, so the sentinel must not be rebuilt per call.
	require.True(t, errors.Is(RetrieveParams{}.ValidateScope(), ErrRetrieveScopeRequired))
}
