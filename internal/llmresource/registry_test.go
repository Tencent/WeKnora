package llmresource

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/require"
)

func TestRegistryRoundTripAndDeduplicate(t *testing.T) {
	r := NewRegistry()
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	encoded := r.EncodeText("![a](" + ref + ") and " + ref)
	require.Equal(t, "![a](res://0001) and res://0001", encoded)
	require.Equal(t, "![a]("+ref+") and "+ref, r.DecodeText(encoded))
}

func TestRegistryAliasesLegacyPhysicalReferencesDuringRollout(t *testing.T) {
	r := NewRegistry()
	ref := "storage://c0d93536-702c-4977-aa5e-fe670073c3cb/local://10000/exports/image.png"
	encoded := r.EncodeText("![image](" + ref + ")")
	require.Equal(t, "![image](res://0001)", encoded)
	require.Equal(t, "![image]("+ref+")", r.DecodeText(encoded))
}

func TestRegistryEncodesMessageCopies(t *testing.T) {
	r := NewRegistry()
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	original := []chat.Message{{Role: "tool", Content: ref}}
	encoded := r.EncodeMessages(original)
	require.Equal(t, ref, original[0].Content)
	require.Equal(t, "res://0001", encoded[0].Content)
}

func TestStreamDecoderRestoresSplitAlias(t *testing.T) {
	r := NewRegistry()
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	require.Equal(t, "res://0001", r.EncodeText(ref))
	d := NewStreamDecoder(r)
	require.Equal(t, "before ", d.Feed("before res://0"))
	require.Equal(t, ref+" afte", d.Feed("001 after"))
	require.Equal(t, "r", d.Flush())
}

func TestOrphanAliasesReportsUnresolvableTokens(t *testing.T) {
	r := NewRegistry()
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	require.Equal(t, "res://0001", r.EncodeText(ref))

	// Known alias resolves and leaves no orphan once decoded.
	require.Nil(t, r.OrphanAliases(r.DecodeText("see res://0001")))

	// A reference the registry never assigned is reported (deduplicated).
	require.Equal(t, []string{"res://0099"}, r.OrphanAliases("look at res://0099 and res://0099"))
}

func TestOrphanAliasesNilRegistry(t *testing.T) {
	var r *Registry
	require.Equal(t, []string{"res://0001"}, r.OrphanAliases("res://0001"))
}
