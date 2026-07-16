package docparser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPDocumentReaderArtifactIdentityTracksReconnect(t *testing.T) {
	reader, err := NewHTTPDocumentReader("https://docreader-one.example.com/")
	require.NoError(t, err)
	first := reader.ArtifactIdentity()
	require.NotEmpty(t, first)

	require.NoError(t, reader.Reconnect("https://docreader-two.example.com/"))
	assert.NotEqual(t, first, reader.ArtifactIdentity())
}

func TestGRPCDocumentReaderArtifactIdentityUsesAddress(t *testing.T) {
	reader := &GRPCDocumentReader{addr: "docreader:50051"}
	assert.NotEmpty(t, reader.ArtifactIdentity())
}
