package modelcontext

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/require"
)

func TestRegistryRoundTripAndDeduplicate(t *testing.T) {
	r := newResourceRegistry()
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	encoded := r.EncodeText("![a](" + ref + ") and " + ref)
	require.Equal(t, "![a](res://0001) and res://0001", encoded)
	require.Equal(t, "![a]("+ref+") and "+ref, r.DecodeText(encoded))
}

func TestRegistryAliasesLegacyPhysicalReferencesDuringRollout(t *testing.T) {
	r := newResourceRegistry()
	ref := "storage://c0d93536-702c-4977-aa5e-fe670073c3cb/local://10000/exports/image.png"
	encoded := r.EncodeText("![image](" + ref + ")")
	require.Equal(t, "![image](res://0001)", encoded)
	require.Equal(t, "![image]("+ref+")", r.DecodeText(encoded))
}

func TestRegistryAliasesWikiSummarySlug(t *testing.T) {
	r := newResourceRegistry()
	slug := "summary/07a20bb1-a662-47cf-9929-06fb5d5b5b5e"
	// Inside a [[slug|display]] link the model must copy verbatim.
	encoded := r.EncodeText("see [[" + slug + "|Foo.md - Summary]]")
	require.Equal(t, "see [[res://0001|Foo.md - Summary]]", encoded)
	require.Equal(t, "see [["+slug+"|Foo.md - Summary]]", r.DecodeText(encoded))

	// The same slug as a tool-call argument value resolves to one handle.
	require.Equal(t, "res://0001", r.EncodeText(slug))
}

func TestRegistryLeavesEntitySlugUntouched(t *testing.T) {
	r := newResourceRegistry()
	// Entity slugs are low-entropy and semantically meaningful; not aliased.
	entity := "[[entity/weknora-error-log|WeKnora 试错记录]]"
	require.Equal(t, entity, r.EncodeText(entity))
}

func TestRegistryEncodesMessageCopies(t *testing.T) {
	r := newResourceRegistry()
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	original := []chat.Message{{Role: "tool", Content: ref}}
	encoded := r.EncodeMessages(original)
	require.Equal(t, ref, original[0].Content)
	require.Equal(t, "res://0001", encoded[0].Content)
}

func TestStreamDecoderRestoresSplitAlias(t *testing.T) {
	r := newResourceRegistry()
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	require.Equal(t, "res://0001", r.EncodeText(ref))
	d := newResourceStreamDecoder(r)
	require.Equal(t, "before ", d.Feed("before res://0"))
	require.Equal(t, ref+" afte", d.Feed("001 after"))
	require.Equal(t, "r", d.Flush())
}

func TestStreamDecoderRestoresAliasSplitBeforeScheme(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	r := newResourceRegistry()
	require.Equal(t, "res://0001", r.EncodeText(ref))
	d := newResourceStreamDecoder(r)
	require.Equal(t, "see ", d.Feed("see re"))
	require.Equal(t, ref, d.Feed("s://0001"))
	require.Empty(t, d.Flush())
}

func TestOrphanAliasesReportsUnresolvableTokens(t *testing.T) {
	r := newResourceRegistry()
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	require.Equal(t, "res://0001", r.EncodeText(ref))

	// Known handle resolves and leaves no orphan once decoded.
	require.Nil(t, r.OrphanHandles(r.DecodeText("see res://0001")))

	// A reference the registry never assigned is reported (deduplicated).
	require.Equal(t, []string{"res://0099"}, r.OrphanHandles("look at res://0099 and res://0099"))
}

func TestStripOrphanAliasesRemovesIncompleteNumericHandle(t *testing.T) {
	r := newResourceRegistry()
	require.Equal(t, "broken ", r.StripOrphanHandles("broken res://1"))
}

func TestOrphanAliasesNilRegistry(t *testing.T) {
	var r *resourceRegistry
	require.Equal(t, []string{"res://0001"}, r.OrphanHandles("res://0001"))
}
