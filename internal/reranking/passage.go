package reranking

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// --- Passage cleaning for rerank ---
//
// Rerank models work on semantic text similarity. Markdown formatting, raw URLs,
// image references, table separators, and other structural syntax are noise that
// can dilute the semantic signal. The functions below strip this noise before
// passages are sent to the rerank model.

var (
	// reMarkdownImage matches ![alt](url) — the entire construct is noise.
	// URL group supports one level of balanced parentheses.
	reMarkdownImage = regexp.MustCompile(`!\[[^\]]*\]\([^()\s]*(?:\([^)]*\)[^()\s]*)*\)`)
	// reLinkedImage matches [![alt](img_url)](link_url) — unwrap to ![alt](img_url)
	// so that the subsequent reMarkdownImage pass can remove the image.
	reLinkedImage = regexp.MustCompile(
		`\[!\[([^\]]*)\]\(([^()\s]*(?:\([^)]*\)[^()\s]*)*)\)\]` +
			`\([^()\s]*(?:\([^)]*\)[^()\s]*)*\)`,
	)
	// reMarkdownLink matches [text](url) — we keep the text, drop the URL.
	// URL group supports one level of balanced parentheses.
	reMarkdownLink = regexp.MustCompile(`\[([^\]]+)\]\([^()\s]*(?:\([^)]*\)[^()\s]*)*\)`)
	// reRawURL matches standalone http(s) URLs.
	reRawURL = regexp.MustCompile(`https?://[^\s)\]>]+`)
	// reCodeBlock captures the semantic body of fenced code blocks.
	reCodeBlock = regexp.MustCompile("(?s)```[^\\r\\n]*\\r?\\n(.*?)\\r?\\n?```")
	// reLatexBlock captures the semantic body of block-level LaTeX ($$...$$).
	reLatexBlock = regexp.MustCompile(`(?s)\$\$(.*?)\$\$`)
	// reTableSep matches table separator rows like |---|---|.
	// Uses [ \t] instead of \s to avoid consuming newlines across rows.
	reTableSep = regexp.MustCompile(`(?m)^[ \t]*\|[ \t:|-]+\|[ \t]*$`)
	// reTableRow matches markdown table data rows like | col1 | col2 |.
	// Uses [ \t] instead of \s to avoid consuming newlines across rows.
	reTableRow = regexp.MustCompile(`(?m)^[ \t]*\|(.+?)\|[ \t]*$`)
	// reHeadingPrefix matches leading # markers in headings.
	reHeadingPrefix = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	// reBlockquote matches leading > markers.
	reBlockquote = regexp.MustCompile(`(?m)^>\s?`)
	// reBoldItalic3 matches ***text*** wrappers (must come before 2 and 1).
	reBoldItalic3 = regexp.MustCompile(`\*{3}(.+?)\*{3}`)
	// reBoldItalic2 matches **text** wrappers.
	reBoldItalic2 = regexp.MustCompile(`\*{2}(.+?)\*{2}`)
	// reBoldItalic1 matches *text* wrappers.
	reBoldItalic1 = regexp.MustCompile(`\*(.+?)\*`)
	// reExcessiveNewlines collapses 3+ consecutive newlines into 2.
	reExcessiveNewlines = regexp.MustCompile(`\n{3,}`)
	// reListMarker matches unordered (- , * ) and ordered (1. ) list prefixes.
	reListMarker = regexp.MustCompile(`(?m)^[\t ]*(?:[-*+]|\d+\.)\s+`)
	// reHTMLTag matches HTML tags like <br>, <div class="...">, etc.
	reHTMLTag = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
)

// CleanPassage strips markdown/structural noise from text to produce a clean
// semantic passage for the rerank model. The cleaning is designed to preserve
// all meaningful semantic content while removing formatting that would
// confuse text-similarity scoring.
func CleanPassage(text string) string {
	// 1. Unwrap code blocks so code-only candidates remain rerankable.
	text = reCodeBlock.ReplaceAllString(text, "$1")
	// 2. Unwrap LaTeX blocks so formula-only candidates remain rerankable.
	text = reLatexBlock.ReplaceAllString(text, "$1")
	// 3. Remove HTML tags
	text = reHTMLTag.ReplaceAllString(text, "")
	// 3.5. Unwrap nested [![alt](img_url)](link_url) → ![alt](img_url)
	//      so that the next step removes the full construct cleanly.
	text = reLinkedImage.ReplaceAllString(text, "![$1]($2)")
	// 4. Remove markdown image references entirely
	text = reMarkdownImage.ReplaceAllString(text, "")
	// 5. Convert markdown links to just their display text
	text = reMarkdownLink.ReplaceAllString(text, "$1")
	// 6. Remove standalone raw URLs
	text = reRawURL.ReplaceAllString(text, "")
	// 7. Remove table separator rows
	text = reTableSep.ReplaceAllString(text, "")
	// 7.5. Convert table data rows to plain text (strip | delimiters)
	text = reTableRow.ReplaceAllStringFunc(text, func(match string) string {
		inner := reTableRow.FindStringSubmatch(match)
		if len(inner) < 2 {
			return match
		}
		cells := strings.Split(inner[1], "|")
		var parts []string
		for _, cell := range cells {
			cell = strings.TrimSpace(cell)
			if cell != "" {
				parts = append(parts, cell)
			}
		}
		return strings.Join(parts, ", ")
	})
	// 8. Strip heading markers but keep heading text
	text = reHeadingPrefix.ReplaceAllString(text, "")
	// 9. Strip blockquote markers
	text = reBlockquote.ReplaceAllString(text, "")
	// 10. Unwrap bold/italic markers, keeping inner text (order: *** before ** before *)
	text = reBoldItalic3.ReplaceAllString(text, "$1")
	text = reBoldItalic2.ReplaceAllString(text, "$1")
	text = reBoldItalic1.ReplaceAllString(text, "$1")
	// 11. Strip list markers
	text = reListMarker.ReplaceAllString(text, "")
	// 12. Collapse excessive newlines
	text = reExcessiveNewlines.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}

// EnrichedPassage is the cleaned chunk body plus the image captions, OCR text
// and generated questions attached to it. MMR compares candidates on this
// text, so chunks of one document are not made similar by a shared title.
func EnrichedPassage(ctx context.Context, result *types.SearchResult) string {
	combinedText := CleanPassage(result.Content)
	var enrichments []string

	if result.ImageInfo != "" {
		var imageInfos []types.ImageInfo
		if err := json.Unmarshal([]byte(result.ImageInfo), &imageInfos); err != nil {
			logger.Warnf(ctx, "[Rerank] Failed to parse image info of chunk %s: %v", result.ID, err)
		} else {
			for _, img := range imageInfos {
				if img.Caption != "" {
					enrichments = append(enrichments, img.Caption)
				}
				if img.OCRText != "" {
					enrichments = append(enrichments, img.OCRText)
				}
			}
		}
	}

	if len(result.ChunkMetadata) > 0 {
		var docMeta types.DocumentChunkMetadata
		if err := json.Unmarshal(result.ChunkMetadata, &docMeta); err != nil {
			logger.Warnf(ctx, "[Rerank] Failed to parse chunk metadata of chunk %s: %v", result.ID, err)
		} else if questionStrings := docMeta.GetQuestionStrings(); len(questionStrings) > 0 {
			enrichments = append(enrichments, strings.Join(questionStrings, "; "))
		}
	}

	if len(enrichments) == 0 {
		return combinedText
	}
	if combinedText != "" {
		combinedText += "\n\n"
	}
	return combinedText + strings.Join(enrichments, "\n")
}

// ModelPassage is the text the rerank model scores: the document title
// followed by the enriched chunk. A chunk rarely restates what its document
// is about, so without the title a passage from "Show HN: Echo - ... using
// open-weight models" scored 0.002 against "Echo open-weight models reduce
// cost" and 0.45 with it. FAQ entries carry their own question instead.
//
// An empty enriched body stays empty: a title alone is not evidence that
// the chunk answers anything.
func ModelPassage(ctx context.Context, result *types.SearchResult) string {
	passage := EnrichedPassage(ctx, result)
	title := strings.TrimSpace(result.KnowledgeTitle)
	if strings.TrimSpace(passage) == "" || title == "" || result.ChunkType == string(types.ChunkTypeFAQ) {
		return passage
	}
	return title + "\n\n" + passage
}
