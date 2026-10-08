package modelcontext

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
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

// Aliases belong to one execution, and never follow mutable file names.
func TestResourceVersionsStayImmutableAcrossRounds(t *testing.T) {
	old := "resource://dHZ_fFslfs0GgJGaJZGjGA"
	latest := "resource://4N1nAo-FZZoDEExDQz2yoA"
	r := newResourceRegistry()
	r.EncodeText("resource://aaaaaaaaaaaaaaaaaaaaaa resource://bbbbbbbbbbbbbbbbbbbbbb")
	require.Equal(t, "res://0003", r.EncodeText(old))
	require.Equal(t, "res://0004", r.EncodeText(latest))
	require.Equal(t, old, r.DecodeText("res://0003"))
	require.Equal(t, latest, r.DecodeText("res://0004"))
	require.Equal(t, "res://0003", r.EncodeText(old))
	nextRequest := newResourceRegistry()
	require.Equal(t, "res://0001", nextRequest.EncodeText(latest))
	require.Equal(t, old, r.DecodeText("res://0003"))
}

func TestEncodeMessagesDropsSignatureOnlyWhenReasoningIsRewritten(t *testing.T) {
	r := newResourceRegistry()
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	messages := []chat.Message{
		{
			Role:               "assistant",
			Content:            "answer",
			ReasoningContent:   "I looked at " + ref,
			ReasoningSignature: "anthropic-messages:sig-1",
		},
		{
			Role:               "assistant",
			Content:            "see " + ref,
			ReasoningContent:   "plain thought with nothing to encode",
			ReasoningSignature: "anthropic-messages:sig-2",
		},
	}

	encoded := r.EncodeMessages(messages)

	// The first message's thinking text was rewritten, so its signature no
	// longer covers what would go on the wire.
	require.Equal(t, "I looked at res://0001", encoded[0].ReasoningContent)
	require.Empty(t, encoded[0].ReasoningSignature)
	// The second one's thinking text is untouched (only Content changed), so
	// the block stays replayable.
	require.Equal(t, "plain thought with nothing to encode", encoded[1].ReasoningContent)
	require.Equal(t, "anthropic-messages:sig-2", encoded[1].ReasoningSignature)
	// Callers' own slice is never mutated.
	require.Equal(t, "anthropic-messages:sig-1", messages[0].ReasoningSignature)
}

func TestEncodeMessagesKeepsGeminiSignatureWhenReasoningIsRewritten(t *testing.T) {
	r := newResourceRegistry()
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	messages := []chat.Message{{
		Role:             "assistant",
		Content:          "answer",
		ReasoningContent: "I looked at " + ref,
		// Gemini replays this signature on the answer text and on each tool
		// call's own metadata; it never replays the reasoning text, so a
		// rewrite here does not invalidate it.
		ReasoningSignature: "google-generative-ai:thought-sig",
	}}

	encoded := r.EncodeMessages(messages)

	require.Equal(t, "I looked at res://0001", encoded[0].ReasoningContent)
	require.Equal(t, "google-generative-ai:thought-sig", encoded[0].ReasoningSignature)
}

func TestDecodeResponseKeepsGeminiSignature(t *testing.T) {
	registry := NewRegistry(true)
	ref := "resource://AbCdEfGhIjKlMnOpQrStUv"
	registry.EncodeMessages([]chat.Message{{Role: "assistant", Content: ref}})

	decoded := &types.ChatResponse{
		Content:            "done",
		ReasoningContent:   "read res://0001",
		ReasoningSignature: "google-generative-ai:thought-sig",
	}
	registry.DecodeResponse(decoded)
	require.Equal(t, "read "+ref, decoded.ReasoningContent)
	require.Equal(t, "google-generative-ai:thought-sig", decoded.ReasoningSignature)
}
