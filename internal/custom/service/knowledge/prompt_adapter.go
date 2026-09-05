package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// NativePromptInput is the identity-only context allowed when adapting the
// native WeKnora Wiki extraction prompt. It deliberately has no content,
// subtitle, chunk, or page fields.
type NativePromptInput struct {
	SourceDocumentID     string
	SourceVideoID        string
	TranscriptGeneration string
	InputMode            string
}

// NativePromptTrace contains the adapted prompt and a redacted before/after
// pair suitable for acceptance artifacts. Prompt bodies are never persisted
// by this adapter.
type NativePromptTrace struct {
	Prompt               string `json:"prompt"`
	BeforeRedacted       string `json:"before_redacted"`
	AfterRedacted        string `json:"after_redacted"`
	SourceDocumentDigest string `json:"source_document_digest"`
}

const nativePromptAdapterMarker = "<video_knowledge_prompt_adapter>"

// AdaptNativePrompt adds the P3 video-knowledge contract to a native
// WeKnora prompt while leaving the native Map-Reduce and Wiki writer intact.
// The adapter is fail-closed: only full-document identity input is accepted,
// and prompts containing a chunk payload are rejected.
func AdaptNativePrompt(prompt string, input NativePromptInput) (NativePromptTrace, error) {
	if strings.TrimSpace(prompt) == "" {
		return NativePromptTrace{}, fmt.Errorf("native prompt is required")
	}
	if strings.TrimSpace(input.SourceDocumentID) == "" || strings.TrimSpace(input.SourceVideoID) == "" || strings.TrimSpace(input.TranscriptGeneration) == "" {
		return NativePromptTrace{}, fmt.Errorf("native prompt source identity is incomplete")
	}
	mode := strings.TrimSpace(input.InputMode)
	if mode == "" {
		mode = "full_document"
	}
	if mode != "full_document" {
		return NativePromptTrace{}, fmt.Errorf("native prompt adapter requires full_document input mode")
	}
	lowerPrompt := strings.ToLower(prompt)
	for _, marker := range []string{"<chunks", "<subtitle_chunks", "<transcript_chunks", "evidence_chunks"} {
		if strings.Contains(lowerPrompt, marker) {
			return NativePromptTrace{}, fmt.Errorf("native prompt adapter rejects subtitle or chunk payload")
		}
	}
	if strings.Contains(prompt, nativePromptAdapterMarker) {
		return NativePromptTrace{Prompt: prompt, BeforeRedacted: redactPrompt(prompt), AfterRedacted: redactPrompt(prompt), SourceDocumentDigest: digestID(input.SourceDocumentID)}, nil
	}

	digest, err := LoadDefaultFrameworkDigest()
	if err != nil {
		return NativePromptTrace{}, fmt.Errorf("load knowledge framework for prompt adapter: %w", err)
	}

	var rules strings.Builder
	rules.WriteString(nativePromptAdapterMarker)
	rules.WriteString("\nsource_document_id: ")
	rules.WriteString(strings.TrimSpace(input.SourceDocumentID))
	rules.WriteString("\nsource_video_id: ")
	rules.WriteString(strings.TrimSpace(input.SourceVideoID))
	rules.WriteString("\ntranscript_generation: ")
	rules.WriteString(strings.TrimSpace(input.TranscriptGeneration))
	rules.WriteString("\ninput_mode: full_document\n")
	rules.WriteString("\n本体规则：只能输出 entity、concept、methodology、case、insight 五类业务对象；实体子类型只能使用 person、organization、product、technology、industry、place。\n")
	rules.WriteString("最小粒度：一个对象只表达一个主要命题；多个独立命题必须拆分。\n")
	rules.WriteString("证据要求：每个对象和每个结构字段都必须绑定源文档中的真实 evidence ID；没有证据不得输出，不得创造、猜测或改写证据 ID。\n")
	rules.WriteString("输入边界：只读取 source_document_id 对应的整篇源文档；不得读取字幕分块知识 ID，不得把字幕分块正文作为抽取上下文。\n")
	rules.WriteString("流程边界：保留 WeKnora 原生 Map-Reduce、候选合并、页面创建/更新和审计入口；本适配器不生成页面、不写 Wiki、不写 Graph。\n")
	rules.WriteString("允许字段与顺序（以下顺序来自 type-frameworks.md）：\n")
	for _, typ := range digest.Types {
		rules.WriteString("- ")
		rules.WriteString(string(typ.PrimaryType))
		rules.WriteString("（")
		rules.WriteString(typ.Label)
		rules.WriteString("）")
		for _, entry := range digest.Entries {
			if entry.PrimaryType != typ.PrimaryType {
				continue
			}
			if entry.EntitySubType != "" {
				rules.WriteString(" / ")
				rules.WriteString(entry.EntitySubType)
			}
			rules.WriteString(": ")
			for i, field := range entry.Fields {
				if i > 0 {
					rules.WriteString(" -> ")
				}
				rules.WriteString(field.Key)
			}
			rules.WriteString("\n")
		}
	}
	rules.WriteString("</video_knowledge_prompt_adapter>")

	adapted := strings.TrimSpace(prompt) + "\n\n" + rules.String()
	return NativePromptTrace{
		Prompt:               adapted,
		BeforeRedacted:       redactPrompt(prompt),
		AfterRedacted:        redactPrompt(adapted),
		SourceDocumentDigest: digestID(input.SourceDocumentID),
	}, nil
}

func digestID(value string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(hash[:])[:16]
}

// redactPrompt removes variable document/chunk bodies while retaining the
// prompt structure needed for review. IDs are represented by a stable digest.
func redactPrompt(prompt string) string {
	redacted := prompt
	for _, tag := range []string{"content", "chunks", "subtitle_chunks", "document"} {
		open, close := "<"+tag+">", "</"+tag+">"
		searchFrom := 0
		for searchFrom < len(redacted) {
			relStart := strings.Index(redacted[searchFrom:], open)
			if relStart < 0 {
				break
			}
			start := searchFrom + relStart
			if start < 0 {
				break
			}
			relEnd := strings.Index(redacted[start+len(open):], close)
			if relEnd < 0 {
				break
			}
			end := start + len(open) + relEnd + len(close)
			replacement := open + "[REDACTED]" + close
			redacted = redacted[:start] + replacement + redacted[end:]
			searchFrom = start + len(replacement)
		}
	}
	return strings.TrimSpace(redacted)
}
