package knowledge

import (
	"strings"
	"testing"
)

func TestAdaptNativePromptInjectsFrameworkRulesAndKeepsNativeFlow(t *testing.T) {
	trace, err := AdaptNativePrompt("NATIVE_MAP_REDUCE_PROMPT", NativePromptInput{
		SourceDocumentID: "source-doc-1", SourceVideoID: "video-1", TranscriptGeneration: "generation-1", InputMode: "full_document",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"<video_knowledge_prompt_adapter>",
		"source_document_id: source-doc-1",
		"只能输出 entity、concept、methodology、case、insight 五类业务对象",
		"最小粒度",
		"证据要求",
		"保留 WeKnora 原生 Map-Reduce",
		"不写 Wiki、不写 Graph",
		"methodology",
		"input -> steps",
	} {
		if !strings.Contains(trace.Prompt, expected) {
			t.Fatalf("adapted prompt missing %q: %s", expected, trace.Prompt)
		}
	}
	if trace.SourceDocumentDigest == "source-doc-1" || len(trace.SourceDocumentDigest) != 16 {
		t.Fatalf("source document should be represented by a stable digest: %#v", trace)
	}
	if trace.BeforeRedacted != "NATIVE_MAP_REDUCE_PROMPT" || trace.AfterRedacted == "" {
		t.Fatalf("redacted before/after trace missing: %#v", trace)
	}
}

func TestAdaptNativePromptRedactsDocumentBodyFromAcceptanceTrace(t *testing.T) {
	trace, err := AdaptNativePrompt("<document><content>字幕正文秘密</content></document>", NativePromptInput{
		SourceDocumentID: "source-doc-1", SourceVideoID: "video-1", TranscriptGeneration: "generation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(trace.BeforeRedacted, "字幕正文秘密") || strings.Contains(trace.AfterRedacted, "字幕正文秘密") {
		t.Fatalf("prompt trace leaked document body: %#v", trace)
	}
	if !strings.Contains(trace.BeforeRedacted, "[REDACTED]") {
		t.Fatalf("document body was not redacted: %s", trace.BeforeRedacted)
	}
}

func TestAdaptNativePromptRejectsChunkInputAndIncompleteIdentity(t *testing.T) {
	base := NativePromptInput{SourceDocumentID: "source-doc-1", SourceVideoID: "video-1", TranscriptGeneration: "generation-1"}
	if _, err := AdaptNativePrompt("<chunks><c>字幕块正文</c></chunks>", base); err == nil || !strings.Contains(err.Error(), "chunk payload") {
		t.Fatalf("chunk payload should be rejected: %v", err)
	}
	if _, err := AdaptNativePrompt("prompt", NativePromptInput{SourceDocumentID: "source-doc-1"}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("incomplete identity should be rejected: %v", err)
	}
	if _, err := AdaptNativePrompt("prompt", NativePromptInput{SourceDocumentID: "source-doc-1", SourceVideoID: "video-1", TranscriptGeneration: "generation-1", InputMode: "evidence_chunks"}); err == nil || !strings.Contains(err.Error(), "full_document") {
		t.Fatalf("non-full input should be rejected: %v", err)
	}
}

func TestAdaptNativePromptIsByteStableAndIdempotent(t *testing.T) {
	input := NativePromptInput{SourceDocumentID: "source-doc-1", SourceVideoID: "video-1", TranscriptGeneration: "generation-1"}
	a, err := AdaptNativePrompt("prompt", input)
	if err != nil {
		t.Fatal(err)
	}
	b, err := AdaptNativePrompt("prompt", input)
	if err != nil {
		t.Fatal(err)
	}
	if a.Prompt != b.Prompt || a.AfterRedacted != b.AfterRedacted {
		t.Fatal("same adapter input produced different output")
	}
	c, err := AdaptNativePrompt(a.Prompt, input)
	if err != nil {
		t.Fatal(err)
	}
	if c.Prompt != a.Prompt {
		t.Fatal("adapter is not idempotent")
	}
}
