package artifact

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInlineArtifactAllowsSuccessfulEmptyOutput(t *testing.T) {
	key, err := BuildKey(1, testKeyMaterial())
	require.NoError(t, err)
	manifest, err := NewInlineArtifact(key, "empty.v1", nil)
	require.NoError(t, err)

	assert.NotNil(t, manifest.Payload)
	assert.Zero(t, manifest.SizeBytes)
	payload, err := DecodeInline(manifest, key.Lookup, key.OutputSchema, "empty.v1")
	require.NoError(t, err)
	assert.Empty(t, payload)
}

func TestDecodeInlineRejectsCorruptionAndSchemaMismatch(t *testing.T) {
	key, err := BuildKey(1, testKeyMaterial())
	require.NoError(t, err)
	manifest, err := NewInlineArtifact(key, CodecJSONV1, []byte(`{"ok":true}`))
	require.NoError(t, err)

	manifest.Payload[1] = 'X'
	_, err = DecodeInline(manifest, key.Lookup, key.OutputSchema, CodecJSONV1)
	require.ErrorIs(t, err, ErrCorruptArtifact)

	manifest, err = NewInlineArtifact(key, CodecJSONV1, []byte(`{"ok":true}`))
	require.NoError(t, err)
	_, err = DecodeInline(manifest, key.Lookup, "other.v1", CodecJSONV1)
	require.ErrorIs(t, err, ErrCorruptArtifact)
}

func TestEncodeJSONRejectsOwnershipFields(t *testing.T) {
	key, err := BuildKey(1, testKeyMaterial())
	require.NoError(t, err)

	_, err = EncodeJSON(key, map[string]any{
		"value":        "valid provider output",
		"knowledge_id": "live-owner",
	})
	require.ErrorContains(t, err, "ownership field")
}
