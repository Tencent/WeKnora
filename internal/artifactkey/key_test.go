package artifactkey

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateStableAndUnambiguous(t *testing.T) {
	base := KeyInput{Kind: "summary", TenantScope: "tenant:世界", InputDigest: DigestText("正文"), ModelID: "m", ModelRevision: "1", PromptVersion: "p1", ConfigDigest: DigestText("c"), ProducerVersion: "v1"}
	require.Equal(t, Generate(base), Generate(base))

	changes := []KeyInput{
		{Kind: "question", TenantScope: base.TenantScope, InputDigest: base.InputDigest, ModelID: base.ModelID, ModelRevision: base.ModelRevision, PromptVersion: base.PromptVersion, ConfigDigest: base.ConfigDigest, ProducerVersion: base.ProducerVersion},
		{Kind: base.Kind, TenantScope: base.TenantScope, InputDigest: base.InputDigest, ModelID: base.ModelID, ModelRevision: "2", PromptVersion: base.PromptVersion, ConfigDigest: base.ConfigDigest, ProducerVersion: base.ProducerVersion},
		{Kind: base.Kind, TenantScope: base.TenantScope, InputDigest: base.InputDigest, ModelID: base.ModelID, ModelRevision: base.ModelRevision, PromptVersion: "p2", ConfigDigest: base.ConfigDigest, ProducerVersion: base.ProducerVersion},
	}
	for _, changed := range changes {
		require.NotEqual(t, Generate(base), Generate(changed))
	}
	require.NotEqual(t, Generate(KeyInput{Kind: "ab", TenantScope: "c"}), Generate(KeyInput{Kind: "a", TenantScope: "bc"}))
}

func TestDigestConfigCanonicalMapOrderingAndNilSemantics(t *testing.T) {
	a, err := DigestConfig(map[string]any{"b": 2, "a": map[string]any{"y": 2, "x": 1}})
	require.NoError(t, err)
	b, err := DigestConfig(map[string]any{"a": map[string]any{"x": 1, "y": 2}, "b": 2})
	require.NoError(t, err)
	require.Equal(t, a, b)
	nilDigest, _ := DigestConfig(nil)
	emptyDigest, _ := DigestConfig(map[string]any{})
	require.NotEqual(t, nilDigest, emptyDigest)
}
