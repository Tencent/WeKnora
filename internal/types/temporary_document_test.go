package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemporaryDocumentForClientDropsDescriptionCache(t *testing.T) {
	doc := &TemporaryDocument{
		ID:       "doc",
		Metadata: JSON(`{"pages":3,"` + TemporaryDocumentImageDescriptionsKey + `":{"k":{"text":"secret"}}}`),
	}
	client := doc.ForClient()
	require.JSONEq(t, `{"pages":3}`, string(client.Metadata))
	require.Contains(t, string(doc.Metadata), TemporaryDocumentImageDescriptionsKey)

	plain := &TemporaryDocument{Metadata: JSON(`{"pages":3}`)}
	require.JSONEq(t, `{"pages":3}`, string(plain.ForClient().Metadata))
	require.Nil(t, (*TemporaryDocument)(nil).ForClient())
}
