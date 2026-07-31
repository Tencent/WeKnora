package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalDigestIsStableAcrossMapOrder(t *testing.T) {
	left := map[string]any{
		"stage": "embedding",
		"request": map[string]any{
			"input": []any{"alpha", "beta"},
			"model": "text-embedding",
		},
	}
	right := map[string]any{
		"request": map[string]any{
			"model": "text-embedding",
			"input": []any{"alpha", "beta"},
		},
		"stage": "embedding",
	}

	leftDigest, err := CanonicalDigest(left)
	require.NoError(t, err)
	rightDigest, err := CanonicalDigest(right)
	require.NoError(t, err)

	assert.Equal(t, leftDigest, rightDigest)
}

func TestArtifactKeyScrubsSecretFields(t *testing.T) {
	withSecret := KeyMaterial{
		KeyVersion: 1,
		Stage:      "summary",
		DirectInputs: []InputDigest{{
			Name:   "chunk",
			Digest: "abc",
		}},
		Processor: ProcessorIdentity{Name: "openai", Version: "v1"},
		RenderedRequest: map[string]any{
			"messages": []any{map[string]any{"role": "user", "content": "summarize"}},
			"headers": map[string]any{
				"Authorization": "Bearer secret-token",
				"Cookie":        "session=secret",
			},
			"api_key": "secret-api-key",
		},
		EffectiveOptions: map[string]any{"temperature": 0},
		OutputSchema:     "summary.v1",
	}
	withoutSecret := withSecret
	withoutSecret.RenderedRequest = map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "summarize"}},
		"headers":  map[string]any{},
	}

	withSecretDigest, err := BuildKey(withSecret)
	require.NoError(t, err)
	withoutSecretDigest, err := BuildKey(withoutSecret)
	require.NoError(t, err)

	assert.Equal(t, withoutSecretDigest, withSecretDigest)
}

func TestArtifactKeyScrubsSecretStructFields(t *testing.T) {
	type providerOptions struct {
		BaseURL     string `json:"base_url"`
		APIKey      string `json:"api_key"`
		AccessToken string `json:"access_token"`
	}
	withSecret := KeyMaterial{
		KeyVersion:       1,
		Stage:            "vlm_ocr",
		Processor:        ProcessorIdentity{Name: "vlm", Model: "m1"},
		EffectiveOptions: providerOptions{BaseURL: "https://example.test", APIKey: "secret-a", AccessToken: "token-a"},
		OutputSchema:     "ocr.v1",
	}
	withoutSecret := withSecret
	withoutSecret.EffectiveOptions = providerOptions{BaseURL: "https://example.test", APIKey: "secret-b", AccessToken: "token-b"}

	withSecretDigest, err := BuildKey(withSecret)
	require.NoError(t, err)
	withoutSecretDigest, err := BuildKey(withoutSecret)
	require.NoError(t, err)

	assert.Equal(t, withoutSecretDigest, withSecretDigest)
}

func TestArtifactKeyChangesWhenEffectiveOptionsChange(t *testing.T) {
	base := KeyMaterial{
		KeyVersion:       1,
		Stage:            "embedding",
		DirectInputs:     []InputDigest{{Name: "text", Digest: "abc"}},
		Processor:        ProcessorIdentity{Name: "embedder", Version: "v1"},
		RenderedRequest:  map[string]any{"input": "hello", "model": "m1"},
		EffectiveOptions: map[string]any{"dimensions": 1024},
		OutputSchema:     "embedding.v1",
	}
	changed := base
	changed.EffectiveOptions = map[string]any{"dimensions": 1536}

	baseDigest, err := BuildKey(base)
	require.NoError(t, err)
	changedDigest, err := BuildKey(changed)
	require.NoError(t, err)

	assert.NotEqual(t, baseDigest, changedDigest)
}

func TestValidatePayloadRejectsChecksumMismatch(t *testing.T) {
	payload := []byte(`{"ok":true}`)
	checksum := sha256.Sum256(payload)
	encoded := hex.EncodeToString(checksum[:])

	require.NoError(t, ValidatePayload(payload, int64(len(payload)), encoded))
	assert.Error(t, ValidatePayload([]byte(`{"ok":false}`), int64(len(payload)), encoded))
	assert.Error(t, ValidatePayload(payload, int64(len(payload)+1), encoded))
}
