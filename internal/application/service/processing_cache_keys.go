package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

const (
	stableIDNamespaceName = "weknora:processing:v1"
	wikiMapPromptVersion  = "wiki-map-v1"
	vlmOCRPromptVersion   = "vlm-ocr-v1"
	vlmCaptionVersion     = "vlm-caption-v1"
	graphChunkVersion     = "graph-chunk-v1"
	summaryPromptVersion  = "summary-v1"
	questionPromptVersion = "question-v1"
	parseArtifactVersion  = "parse-artifact-v1"
)

var stableIDNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte(stableIDNamespaceName))

func stableHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func stableBytesHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func stableChunkID(knowledgeID string, chunkType types.ChunkType, seq, start, end int, content string, extras ...string) string {
	parts := []string{
		"chunk",
		knowledgeID,
		string(chunkType),
		fmt.Sprintf("%d", seq),
		fmt.Sprintf("%d", start),
		fmt.Sprintf("%d", end),
		strings.TrimSpace(content),
	}
	parts = append(parts, extras...)
	return uuid.NewSHA1(stableIDNamespace, []byte(strings.Join(parts, "\x00"))).String()
}

func stableQuestionID(chunkID, question string, ordinal int) string {
	return uuid.NewSHA1(stableIDNamespace, []byte(strings.Join([]string{
		"question",
		chunkID,
		fmt.Sprintf("%d", ordinal),
		strings.TrimSpace(question),
	}, "\x00"))).String()
}

func stableFAQChunkID(knowledgeID string, meta *types.FAQChunkMetadata, content string) string {
	contentHash := types.CalculateFAQContentHash(meta)
	if contentHash == "" {
		contentHash = normalizedContentHash(content)
	}
	return stableChunkID(knowledgeID, types.ChunkTypeFAQ, 0, 0, 0, content, contentHash)
}

func normalizedContentHash(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	return stableHash(strings.Join(lines, "\n"))
}

func jsonStableHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return stableHash(fmt.Sprintf("%v", v))
	}
	return stableHash(string(b))
}

func processingCacheKey(parts ...string) string {
	return stableHash(parts...)
}

func vlmOCRCacheKey(imageBytes []byte, vlmModelID, sourceType, prompt string) string {
	return processingCacheKey(stableBytesHash(imageBytes), vlmModelID, vlmOCRPromptVersion, sourceType, prompt)
}

func vlmCaptionCacheKey(imageBytes []byte, vlmModelID, prompt string) string {
	return processingCacheKey(stableBytesHash(imageBytes), vlmModelID, vlmCaptionVersion, prompt)
}

var (
	wikiImageURLPattern      = regexp.MustCompile(`<image\s+url="[^"]*">`)
	wikiImageOriginalPattern = regexp.MustCompile(`(?s)<image_original>.*?</image_original>`)
)

func canonicalWikiMapContent(content string) string {
	content = wikiImageURLPattern.ReplaceAllString(content, "<image>")
	content = wikiImageOriginalPattern.ReplaceAllString(content, "<image_original>[image]</image_original>")
	return content
}

func wikiMapCacheKey(
	content, knowledgeBaseID, knowledgeID, lang, extractionGranularity,
	synthesisModelID, promptBundleHash string,
) string {
	return processingCacheKey(
		normalizedContentHash(canonicalWikiMapContent(content)),
		knowledgeBaseID,
		knowledgeID,
		lang,
		extractionGranularity,
		synthesisModelID,
		promptBundleHash,
	)
}

func summaryCacheKey(content, modelID, prompt string, maxTokens int) string {
	return processingCacheKey(
		normalizedContentHash(content),
		modelID,
		summaryPromptVersion,
		prompt,
		fmt.Sprintf("max_tokens:%d", maxTokens),
	)
}

func questionCacheKey(prompt, modelID string) string {
	return processingCacheKey(
		normalizedContentHash(prompt),
		modelID,
		questionPromptVersion,
	)
}

// parseArtifactCacheKey deliberately uses stable parser inputs only. RequestID
// and attempt numbers change on every queued run and would make reparses miss.
func parseArtifactCacheKey(
	fileBytes []byte,
	fileName string,
	fileType string,
	parserEngine string,
	title string,
	parserOverrides map[string]string,
) string {
	return processingCacheKey(
		stableBytesHash(fileBytes),
		strings.TrimSpace(fileName),
		strings.TrimSpace(fileType),
		strings.TrimSpace(parserEngine),
		strings.TrimSpace(title),
		jsonStableHash(parserOverrides),
		parseArtifactVersion,
	)
}
