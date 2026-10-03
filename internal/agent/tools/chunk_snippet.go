package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Shared helpers for the knowledge retrieval tools (search_knowledge,
// read_document, list_documents): snippet extraction, FAQ display, image
// enrichment and the minimal XML escaping used by tool output.

// xmlEscape replaces characters that would break simple XML attribute /
// element values. It is intentionally minimal because the rendered output is
// consumed by the LLM (forgiving parser) rather than a strict XML processor.
func xmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}

func dedupNonEmptyStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// regexMatchesAny reports whether text matches at least one of the compiled
// patterns.
func regexMatchesAny(text string, compiled []*regexp.Regexp) bool {
	if text == "" || len(compiled) == 0 {
		return false
	}
	for _, re := range compiled {
		if re != nil && re.MatchString(text) {
			return true
		}
	}
	return false
}

// extractChunkMatchSnippet returns a preview for tool output. FAQ chunks only
// surface the matched question plus answers from metadata (answers are not
// stored in chunk content for question_only index mode). Other chunk types
// use regex context around the first body match.
func extractChunkMatchSnippet(chunk *types.Chunk, compiled []*regexp.Regexp) string {
	if chunk != nil && chunk.ChunkType == types.ChunkTypeFAQ {
		if s := faqMatchSnippet(chunk, compiled); s != "" {
			return s
		}
	}
	if chunk == nil {
		return ""
	}
	return extractSnippetRegex(chunk.Content, compiled)
}

// extractSnippetRegex returns a short context snippet around the earliest
// regex match across any of the provided compiled patterns. Result is
// compressed to a single line and bounded in length on both sides of the
// match to keep the XML output concise.
func extractSnippetRegex(content string, compiled []*regexp.Regexp) string {
	if content == "" || len(compiled) == 0 {
		return ""
	}

	earliest := -1
	earliestEnd := -1
	for _, re := range compiled {
		if re == nil {
			continue
		}
		loc := re.FindStringIndex(content)
		if loc == nil {
			continue
		}
		if earliest < 0 || loc[0] < earliest {
			earliest = loc[0]
			earliestEnd = loc[1]
		}
	}
	if earliest < 0 {
		return ""
	}

	matchStr := content[earliest:earliestEnd]
	before := content[:earliest]
	after := content[earliestEnd:]

	beforeRunes := []rune(before)
	if len(beforeRunes) > snippetContextRunes {
		beforeRunes = beforeRunes[len(beforeRunes)-snippetContextRunes:]
	}
	afterRunes := []rune(after)
	if len(afterRunes) > snippetContextRunes {
		afterRunes = afterRunes[:snippetContextRunes]
	}
	matchRunes := []rune(matchStr)
	if len(matchRunes) > snippetMaxMatchRunes {
		matchRunes = append(matchRunes[:snippetMaxMatchRunes], []rune("...")...)
	}

	snippet := string(beforeRunes) + string(matchRunes) + string(afterRunes)
	snippet = collapseWhitespace(snippet)
	if len([]rune(snippet)) > snippetMaxTotalRunes {
		snippet = string([]rune(snippet)[:snippetMaxTotalRunes]) + "..."
	}
	return "... " + snippet + " ..."
}

// extractSnippetForQueries tries to produce a short contextual snippet around
// the first occurrence of any token extracted from the provided queries.
// When no token matches (common for fully paraphrased semantic queries) it
// falls back to the leading runes of content so callers always get something
// to scan. The snippet is single-lined and bounded in length.
func extractSnippetForQueries(content string, queries []string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}

	tokens := searchQueryTokens(queries)

	lowered := strings.ToLower(content)
	earliest := -1
	earliestEnd := -1
	for _, tok := range tokens {
		idx := strings.Index(lowered, tok)
		if idx < 0 {
			continue
		}
		end := idx + len(tok)
		if earliest < 0 || idx < earliest {
			earliest = idx
			earliestEnd = end
		}
	}

	if earliest < 0 {
		runes := []rune(content)
		if len(runes) > snippetContextRunes*2 {
			return strings.TrimSpace(string(runes[:snippetContextRunes*2])) + " ..."
		}
		return content
	}

	matchStr := content[earliest:earliestEnd]
	before := content[:earliest]
	after := content[earliestEnd:]

	beforeRunes := []rune(before)
	if len(beforeRunes) > snippetContextRunes {
		beforeRunes = beforeRunes[len(beforeRunes)-snippetContextRunes:]
	}
	afterRunes := []rune(after)
	if len(afterRunes) > snippetContextRunes {
		afterRunes = afterRunes[:snippetContextRunes]
	}

	snippet := collapseWhitespace(string(beforeRunes) + matchStr + string(afterRunes))
	return "... " + snippet + " ..."
}

func collapseWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// faqStandardQuestion returns the FAQ standard question for an FAQ-type chunk,
// or "" for non-FAQ chunks (or when metadata is missing/unparseable). All FAQ
// entries inside one knowledge share the same knowledge title, so surfacing the
// standard question gives each entry a distinct, human-readable identity in
// tool output that would otherwise look like duplicate same-titled chunks.
func faqStandardQuestion(c *types.Chunk) string {
	if c == nil || c.ChunkType != types.ChunkTypeFAQ {
		return ""
	}
	meta, err := c.FAQMetadata()
	if err != nil || meta == nil {
		return ""
	}
	return strings.TrimSpace(meta.StandardQuestion)
}

// enrichChunkImageInfo populates chunk.ImageInfo for a batch of parent text
// chunks by looking up their image_ocr / image_caption children. Chunks that
// already have a non-empty ImageInfo are left untouched.
func enrichChunkImageInfo(
	ctx context.Context,
	chunkRepo interfaces.ChunkRepository,
	tenantID uint64,
	chunks []*types.Chunk,
) {
	if len(chunks) == 0 || chunkRepo == nil {
		return
	}
	ids := make([]string, 0, len(chunks))
	for _, c := range chunks {
		if c.ImageInfo == "" && c.ID != "" {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	infoMap := searchutil.CollectImageInfoByChunkIDs(ctx, chunkRepo, tenantID, ids)
	if len(infoMap) == 0 {
		return
	}
	for _, c := range chunks {
		if c.ImageInfo != "" {
			continue
		}
		if merged, ok := infoMap[c.ID]; ok && merged != "" {
			c.ImageInfo = merged
		}
	}
}

// enrichChunkContent appends Markdown image references (caption + OCR text)
// carried by chunk.ImageInfo to the chunk body.
func enrichChunkContent(c *types.Chunk) string {
	content := c.Content
	if c.ImageInfo != "" {
		var imgInfos []types.ImageInfo
		if err := json.Unmarshal([]byte(c.ImageInfo), &imgInfos); err == nil && len(imgInfos) > 0 {
			var imgBuilder strings.Builder
			for _, img := range imgInfos {
				if imageMarkdown := searchutil.BuildImageInfoMarkdownWithURL(img.URL, &img); imageMarkdown != "" {
					imgBuilder.WriteString("\n")
					imgBuilder.WriteString(imageMarkdown)
				}
			}
			content += imgBuilder.String()
		}
	}
	return content
}

// chunkImageList converts chunk.ImageInfo into the structured image list used
// by tool Data payloads (url / caption / ocr_text per image).
func chunkImageList(imageInfo string) []map[string]string {
	if imageInfo == "" {
		return nil
	}
	var imageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfo), &imageInfos); err != nil || len(imageInfos) == 0 {
		return nil
	}
	imageList := make([]map[string]string, 0, len(imageInfos))
	for _, img := range imageInfos {
		imgData := make(map[string]string)
		if img.URL != "" {
			imgData["url"] = img.URL
		}
		if img.Caption != "" {
			imgData["caption"] = img.Caption
		}
		if img.OCRText != "" {
			imgData["ocr_text"] = img.OCRText
		}
		if len(imgData) > 0 {
			imageList = append(imageList, imgData)
		}
	}
	if len(imageList) == 0 {
		return nil
	}
	return imageList
}

// writeChunkImagesMarkdown appends one Markdown image line per image carried
// by the chunk to the XML output builder.
func writeChunkImagesMarkdown(b *strings.Builder, c *types.Chunk) {
	if c == nil || c.ImageInfo == "" {
		return
	}
	var imageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(c.ImageInfo), &imageInfos); err != nil || len(imageInfos) == 0 {
		return
	}
	for _, img := range imageInfos {
		if imageMarkdown := searchutil.BuildImageInfoMarkdownWithURL(img.URL, &img); imageMarkdown != "" {
			b.WriteString(imageMarkdown)
			b.WriteString("\n")
		}
	}
}

// chunkDataMap builds the structured row shared by read_document Data
// payloads and the modelcontext renderer.
func chunkDataMap(seq int, c *types.Chunk) map[string]interface{} {
	chunkData := map[string]interface{}{
		"seq":             seq,
		"chunk_id":        c.ID,
		"chunk_index":     c.ChunkIndex,
		"content":         c.Content,
		"chunk_type":      c.ChunkType,
		"knowledge_id":    c.KnowledgeID,
		"knowledge_base":  c.KnowledgeBaseID,
		"start_at":        c.StartAt,
		"end_at":          c.EndAt,
		"parent_chunk_id": c.ParentChunkID,
	}
	appendFAQChunkData(chunkData, c)
	normalizeFAQChunkDataMap(chunkData, c)
	if images := chunkImageList(c.ImageInfo); len(images) > 0 {
		chunkData["images"] = images
	}
	return chunkData
}

// writeChunkXML renders one chunk (FAQ entries included) for the UI/log
// Output of read_document.
func writeChunkXML(b *strings.Builder, c *types.Chunk, extraAttrs string) {
	if c == nil {
		return
	}
	if c.ChunkType == types.ChunkTypeFAQ {
		writeFAQEntryXML(b, c)
		writeChunkImagesMarkdown(b, c)
		return
	}
	fmt.Fprintf(b, "<chunk chunk_id=\"%s\" chunk_index=\"%d\" type=\"%s\"%s>\n",
		xmlEscape(c.ID), c.ChunkIndex, c.ChunkType, extraAttrs)
	content := strings.TrimSpace(c.Content)
	if content == "" {
		content = "(empty)"
	}
	fmt.Fprintf(b, "<content>%s</content>\n", content)
	writeChunkImagesMarkdown(b, c)
	b.WriteString("</chunk>\n")
}
