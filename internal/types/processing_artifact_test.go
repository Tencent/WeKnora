package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProcessingArtifactKeyIsTenantStageAndVersionScoped(t *testing.T) {
	base, err := NewProcessingArtifactKey(7, "embedding", 1, []byte("model-a"), []byte("hello"))
	require.NoError(t, err)
	same, err := NewProcessingArtifactKey(7, "embedding", 1, []byte("model-a"), []byte("hello"))
	require.NoError(t, err)
	otherTenant, _ := NewProcessingArtifactKey(8, "embedding", 1, []byte("model-a"), []byte("hello"))
	otherStage, _ := NewProcessingArtifactKey(7, "vlm.ocr", 1, []byte("model-a"), []byte("hello"))
	otherVersion, _ := NewProcessingArtifactKey(7, "embedding", 2, []byte("model-a"), []byte("hello"))

	assert.Equal(t, base, same)
	assert.NotEqual(t, base.TenantID, otherTenant.TenantID)
	assert.NotEqual(t, base.Stage, otherStage.Stage)
	assert.NotEqual(t, base.KeyVersion, otherVersion.KeyVersion)
	assert.Equal(t, base.InputFingerprint, otherTenant.InputFingerprint)
	assert.Equal(t, base.InputFingerprint, otherStage.InputFingerprint)
	assert.Equal(t, base.InputFingerprint, otherVersion.InputFingerprint)
	assert.Regexp(t, `^[0-9a-f]{64}$`, base.InputFingerprint)
}

func TestNewProcessingArtifactKeyUsesLengthPrefixedParts(t *testing.T) {
	a, err := NewProcessingArtifactKey(7, "embedding", 1, []byte("ab"), []byte("c"))
	require.NoError(t, err)
	b, err := NewProcessingArtifactKey(7, "embedding", 1, []byte("a"), []byte("bc"))
	require.NoError(t, err)
	assert.NotEqual(t, a.InputFingerprint, b.InputFingerprint)
}

func TestNewProcessingArtifactKeyRejectsInvalidScope(t *testing.T) {
	_, err := NewProcessingArtifactKey(0, "embedding", 1, []byte("x"))
	assert.Error(t, err)
	_, err = NewProcessingArtifactKey(1, "Embedding Secret", 1, []byte("x"))
	assert.Error(t, err)
	_, err = NewProcessingArtifactKey(1, "embedding", 0, []byte("x"))
	assert.Error(t, err)
}

func TestNewProcessingArtifactKeyHasStableDomainSeparatedFingerprint(t *testing.T) {
	key, err := NewProcessingArtifactKey(7, "embedding", 1, []byte("model-a"), []byte("hello"))
	require.NoError(t, err)

	assert.Equal(t, "af92c53e8683f7ed5f93f7ea9c7ba574d9976a0928ade4ba470d53e5dd2c3510", key.InputFingerprint)
}

func TestNewProcessingArtifactKeyRejectsZeroInputParts(t *testing.T) {
	_, err := NewProcessingArtifactKey(1, "embedding", 1)
	assert.Error(t, err)
}
