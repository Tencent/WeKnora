package artifact

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKeyMaterial() KeyMaterial {
	return KeyMaterial{
		KeyVersion: 1,
		Stage:      "summary",
		DirectInputs: []DirectInput{
			{Role: "chunk", Digest: SHA256Hex([]byte("one"))},
			{Role: "chunk", Digest: SHA256Hex([]byte("two"))},
		},
		Processor: ProcessorIdentity{
			ModelID:          "model-1",
			ModelName:        "chat-model",
			Source:           "openai",
			Provider:         "compatible",
			EndpointIdentity: "https://example.com/v1",
			Revision:         "weights-v1",
			Parameters: map[string]any{
				"temperature": 0.3,
			},
		},
		RenderedRequest: []map[string]string{
			{"role": "system", "content": "Summarize exactly."},
			{"role": "user", "content": " alpha \r\n beta "},
		},
		Options: map[string]any{
			"thinking": false,
		},
		CanonicalizerVersion: CanonicalJSONVersion,
		OutputSchemaVersion:  "summary.v1",
	}
}

func TestCanonicalJSONStableMapOrderAndNumbers(t *testing.T) {
	left := map[string]any{"z": 1.0, "a": map[string]any{"b": true, "a": -0.0}}
	right := json.RawMessage(`{"a":{"a":0,"b":true},"z":1}`)

	leftJSON, err := CanonicalJSON(left)
	require.NoError(t, err)
	rightJSON, err := CanonicalJSON(right)
	require.NoError(t, err)

	assert.Equal(t, `{"a":{"a":0,"b":true},"z":1}`, string(leftJSON))
	assert.Equal(t, leftJSON, rightJSON)
}

func TestCanonicalJSONRejectsNonFiniteNumbers(t *testing.T) {
	_, err := CanonicalJSON(map[string]any{"value": math.NaN()})
	require.Error(t, err)
}

func TestBuildKeyExactInvalidationAndOrderedInputs(t *testing.T) {
	base := testKeyMaterial()
	original, err := BuildKey(7, base)
	require.NoError(t, err)

	unchanged, err := BuildKey(7, testKeyMaterial())
	require.NoError(t, err)
	assert.Equal(t, original.Lookup.ArtifactKey, unchanged.Lookup.ArtifactKey)
	assert.Equal(t, original.ProcessorDigest, unchanged.ProcessorDigest)

	reordered := testKeyMaterial()
	reordered.DirectInputs[0], reordered.DirectInputs[1] = reordered.DirectInputs[1], reordered.DirectInputs[0]
	reorderedKey, err := BuildKey(7, reordered)
	require.NoError(t, err)
	assert.NotEqual(t, original.Lookup.ArtifactKey, reorderedKey.Lookup.ArtifactKey)

	requestChanged := testKeyMaterial()
	requestChanged.RenderedRequest.([]map[string]string)[1]["content"] = "alpha\nbeta"
	requestKey, err := BuildKey(7, requestChanged)
	require.NoError(t, err)
	assert.NotEqual(t, original.Lookup.ArtifactKey, requestKey.Lookup.ArtifactKey)

	optionsChanged := testKeyMaterial()
	optionsChanged.Options.(map[string]any)["thinking"] = true
	optionsKey, err := BuildKey(7, optionsChanged)
	require.NoError(t, err)
	assert.NotEqual(t, original.Lookup.ArtifactKey, optionsKey.Lookup.ArtifactKey)

	schemaChanged := testKeyMaterial()
	schemaChanged.OutputSchemaVersion = "summary.v2"
	schemaKey, err := BuildKey(7, schemaChanged)
	require.NoError(t, err)
	assert.NotEqual(t, original.Lookup.ArtifactKey, schemaKey.Lookup.ArtifactKey)
}

func TestBuildKeyTenantBoundaryIsExplicit(t *testing.T) {
	material := testKeyMaterial()
	first, err := BuildKey(7, material)
	require.NoError(t, err)
	second, err := BuildKey(8, material)
	require.NoError(t, err)

	assert.Equal(t, first.Lookup.ArtifactKey, second.Lookup.ArtifactKey)
	assert.NotEqual(t, first.Lookup, second.Lookup)
}

func TestBuildKeyRejectsSecretsAndSignedEndpoints(t *testing.T) {
	withSecret := testKeyMaterial()
	withSecret.Options = map[string]any{"api_key": "do-not-hash"}
	_, err := BuildKey(7, withSecret)
	require.ErrorContains(t, err, "secret field")

	withSignedEndpoint := testKeyMaterial()
	withSignedEndpoint.Processor.EndpointIdentity = "https://example.com/v1?signature=secret"
	_, err = BuildKey(7, withSignedEndpoint)
	require.ErrorContains(t, err, "query parameters")
}

func TestDownstreamKeyDependsOnUpstreamOutputDigestOnly(t *testing.T) {
	first := testKeyMaterial()
	first.DirectInputs = []DirectInput{{Role: "parse", Digest: SHA256Hex([]byte("canonical-output"))}}
	first.RenderedRequest = "downstream prompt"
	firstKey, err := BuildKey(7, first)
	require.NoError(t, err)

	second := testKeyMaterial()
	second.DirectInputs = []DirectInput{{Role: "parse", Digest: SHA256Hex([]byte("canonical-output"))}}
	second.RenderedRequest = "downstream prompt"
	secondKey, err := BuildKey(7, second)
	require.NoError(t, err)

	assert.Equal(t, firstKey.Lookup.ArtifactKey, secondKey.Lookup.ArtifactKey)
}
