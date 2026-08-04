package searchutil

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MarkdownImageRegex matches Markdown image links: ![alt](url)
var MarkdownImageRegex = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

// HTMLImageSrcRegex matches an HTML <img> tag with a quoted src attribute.
// Documents that rely on surrounding markup for layout embed their screenshots
// this way, so image-derived text has to be matched back to these tags as well
// as to Markdown image links.
//
// Submatches: 1 = attributes before src, 2 = the src value, 3 = attributes
// after. Callers index the value through HTMLImageSrcURLGroup rather than
// assuming a position.
//
// src must be preceded by whitespace so that data-src and other hyphenated
// attribute names are not mistaken for it — matching those would capture a
// lazy-loading placeholder and leave the real src unvisited. An unquoted src
// and a srcset-only tag are deliberately out of scope; both are rare in
// authored documents and matching them reliably needs an HTML parser.
//
// Both this package and the document parser share this one definition. Two
// copies would be free to drift, and an image the parser stores but the
// enricher cannot match back is exactly the kind of gap this exists to close.
var HTMLImageSrcRegex = regexp.MustCompile(`(?i)<img\b([^>]*?)\ssrc\s*=\s*['"]([^'"]+)['"]([^>]*)>`)

// HTMLImageSrcURLGroup is the submatch index of the src value in
// HTMLImageSrcRegex.
const HTMLImageSrcURLGroup = 2

// CollectImageInfoByChunkIDs collects merged image_info JSON for each given
// chunk ID by querying child chunks (image_ocr / image_caption). It supports
// two-level resolution:
//   - If chunkIDs are text chunks, their direct children are image chunks → one query.
//   - If chunkIDs are parent_text chunks, their children are text chunks
//     whose children are image chunks → two queries.
//
// Returns a map of input chunkID → merged image_info JSON string.
func CollectImageInfoByChunkIDs(
	ctx context.Context,
	chunkRepo interfaces.ChunkRepository,
	tenantID uint64,
	chunkIDs []string,
) map[string]string {
	if len(chunkIDs) == 0 {
		return nil
	}

	children, err := chunkRepo.ListChunksByParentIDs(ctx, tenantID, chunkIDs)
	if err != nil || len(children) == 0 {
		return nil
	}

	type imageAgg struct {
		byURL map[string]types.ImageInfo
	}
	aggMap := make(map[string]*imageAgg)

	addInfo := func(targetID string, child *types.Chunk) {
		if child.ImageInfo == "" {
			return
		}
		var infos []types.ImageInfo
		if err := json.Unmarshal([]byte(child.ImageInfo), &infos); err != nil || len(infos) == 0 {
			return
		}
		agg, ok := aggMap[targetID]
		if !ok {
			agg = &imageAgg{byURL: make(map[string]types.ImageInfo)}
			aggMap[targetID] = agg
		}
		for _, info := range infos {
			key := info.URL
			if key == "" {
				key = info.OriginalURL
			}
			if key == "" {
				continue
			}
			existing, exists := agg.byURL[key]
			if !exists {
				agg.byURL[key] = info
			} else {
				if info.OCRText != "" {
					existing.OCRText = info.OCRText
				}
				if info.Caption != "" {
					existing.Caption = info.Caption
				}
				agg.byURL[key] = existing
			}
		}
	}

	var textChildIDs []string
	textToParent := make(map[string]string)

	for _, child := range children {
		switch child.ChunkType {
		case types.ChunkTypeImageOCR, types.ChunkTypeImageCaption:
			addInfo(child.ParentChunkID, child)
		case types.ChunkTypeText:
			textChildIDs = append(textChildIDs, child.ID)
			textToParent[child.ID] = child.ParentChunkID
		}
	}

	if len(textChildIDs) > 0 {
		grandChildren, err := chunkRepo.ListChunksByParentIDs(ctx, tenantID, textChildIDs)
		if err == nil {
			for _, gc := range grandChildren {
				if gc.ChunkType != types.ChunkTypeImageOCR && gc.ChunkType != types.ChunkTypeImageCaption {
					continue
				}
				if parentTextID, ok := textToParent[gc.ParentChunkID]; ok {
					addInfo(parentTextID, gc)
				}
			}
		}
	}

	out := make(map[string]string, len(aggMap))
	for id, agg := range aggMap {
		if len(agg.byURL) == 0 {
			continue
		}
		merged := make([]types.ImageInfo, 0, len(agg.byURL))
		for _, info := range agg.byURL {
			merged = append(merged, info)
		}
		data, err := json.Marshal(merged)
		if err != nil {
			continue
		}
		out[id] = string(data)
	}
	return out
}

// EnrichSearchResultsImageInfo fills in ImageInfo for SearchResults that have
// none by batch-querying child image chunks.
func EnrichSearchResultsImageInfo(
	ctx context.Context,
	chunkRepo interfaces.ChunkRepository,
	tenantID uint64,
	results []*types.SearchResult,
) {
	var chunkIDs []string
	seen := make(map[string]bool)
	for _, r := range results {
		if r.ImageInfo != "" {
			continue
		}
		if !seen[r.ID] {
			seen[r.ID] = true
			chunkIDs = append(chunkIDs, r.ID)
		}
	}
	if len(chunkIDs) == 0 {
		return
	}

	infoMap := CollectImageInfoByChunkIDs(ctx, chunkRepo, tenantID, chunkIDs)
	if len(infoMap) == 0 {
		return
	}

	for _, r := range results {
		if r.ImageInfo != "" {
			continue
		}
		if merged, ok := infoMap[r.ID]; ok {
			r.ImageInfo = merged
		}
	}
}

// MergeImageInfoJSON combines per-chunk image_info JSON strings (from
// CollectImageInfoByChunkIDs) into a single JSON array, deduplicating by URL.
func MergeImageInfoJSON(perChunk map[string]string) string {
	if len(perChunk) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	var all []types.ImageInfo
	for _, raw := range perChunk {
		var infos []types.ImageInfo
		if err := json.Unmarshal([]byte(raw), &infos); err != nil {
			continue
		}
		for _, info := range infos {
			key := info.URL
			if key == "" {
				key = info.OriginalURL
			}
			if key != "" && !seen[key] {
				seen[key] = true
				all = append(all, info)
			}
		}
	}
	if len(all) == 0 {
		return ""
	}
	data, err := json.Marshal(all)
	if err != nil {
		return ""
	}
	return string(data)
}

// EnrichContentWithImageInfo embeds image info as XML tags into text content.
// Inline Markdown image links get wrapped in <image> with <image_caption> / <image_ocr>;
// images not found in content are appended as <image> blocks.
func EnrichContentWithImageInfo(content string, imageInfoJSON string) string {
	var imageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfoJSON), &imageInfos); err != nil {
		return content
	}
	if len(imageInfos) == 0 {
		return content
	}

	imageInfoMap := make(map[string]*types.ImageInfo)
	for i := range imageInfos {
		if imageInfos[i].URL != "" {
			imageInfoMap[imageInfos[i].URL] = &imageInfos[i]
		}
		if imageInfos[i].OriginalURL != "" {
			imageInfoMap[imageInfos[i].OriginalURL] = &imageInfos[i]
		}
	}

	matches := MarkdownImageRegex.FindAllStringSubmatch(content, -1)
	processedURLs := make(map[string]bool)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		imgURL := match[2]
		processedURLs[imgURL] = true

		imgInfo, found := imageInfoMap[imgURL]
		var b strings.Builder
		b.WriteString(fmt.Sprintf("<image url=\"%s\">\n", imgURL))
		b.WriteString(fmt.Sprintf("<image_original>%s</image_original>\n", match[0]))
		if found && imgInfo != nil {
			b.WriteString(BuildImageInfoXML(imgInfo))
		}
		b.WriteString("</image>")
		content = strings.Replace(content, match[0], b.String(), 1)
	}

	var extras []string
	for _, imgInfo := range imageInfos {
		if processedURLs[imgInfo.URL] || processedURLs[imgInfo.OriginalURL] {
			continue
		}
		url := imgInfo.URL
		if url == "" {
			url = imgInfo.OriginalURL
		}
		if block := BuildImageInfoXMLWithURL(url, &imgInfo); block != "" {
			extras = append(extras, block)
		}
	}
	if len(extras) > 0 {
		if content != "" {
			content += "\n"
		}
		content += strings.Join(extras, "\n")
	}
	return content
}

// EnrichContentWithImageInfoForChat enriches matching Markdown images with
// caption / OCR text while keeping the image itself as Markdown. Chat context is
// deliberately answer-ready: if a model copies a relevant image from its
// context, the copied content should still render instead of leaking the
// internal <image> XML protocol into the answer.
//
// Only images with a matching image_info entry are enriched. This avoids adding
// every parent thumbnail when only a few pages were retrieved, and skips orphan
// image_info extras.
func EnrichContentWithImageInfoForChat(content string, imageInfoJSON string) string {
	var imageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfoJSON), &imageInfos); err != nil {
		return content
	}
	if len(imageInfos) == 0 {
		return content
	}

	imageInfoMap := make(map[string]*types.ImageInfo)
	for i := range imageInfos {
		if imageInfos[i].URL != "" {
			imageInfoMap[imageInfos[i].URL] = &imageInfos[i]
		}
		if imageInfos[i].OriginalURL != "" {
			imageInfoMap[imageInfos[i].OriginalURL] = &imageInfos[i]
		}
	}

	// Screenshots embedded as HTML carry their image_info the same way as
	// Markdown links: without covering both, a document that keeps its images in
	// <img> tags reaches the model as bare markup with the caption and OCR text
	// stripped out, even though the analysis ran and the entry is right here.
	//
	// Both syntaxes are located against the ORIGINAL content and spliced in one
	// right-to-left pass. Running one regex over the other's output would let a
	// metadata block that itself quotes an image reference be rescanned, cutting
	// the OCR sentence in half and injecting the same block twice.
	type injection struct {
		at   int
		text string
	}
	var injections []injection

	// trim is applied to HTML only. An attribute value may be padded, while a
	// Markdown target is looked up exactly as written so that the keys this
	// function matches on stay the ones it has always matched on.
	appendFor := func(locs [][]int, urlGroup int, trim bool) {
		for _, loc := range locs {
			start, end := loc[2*urlGroup], loc[2*urlGroup+1]
			if start < 0 {
				continue
			}
			key := content[start:end]
			if trim {
				key = strings.TrimSpace(key)
			}
			imgInfo, found := imageInfoMap[key]
			if !found || imgInfo == nil {
				continue
			}
			metadata := buildImageInfoMarkdownMetadata(imgInfo)
			if metadata == "" {
				continue
			}
			injections = append(injections, injection{at: loc[1], text: "\n\n" + metadata})
		}
	}
	appendFor(MarkdownImageRegex.FindAllStringSubmatchIndex(content, -1), 2, false)
	appendFor(HTMLImageSrcRegex.FindAllStringSubmatchIndex(content, -1), HTMLImageSrcURLGroup, true)

	sort.Slice(injections, func(i, j int) bool { return injections[i].at > injections[j].at })
	for _, inj := range injections {
		content = content[:inj.at] + inj.text + content[inj.at:]
	}
	return content
}

// buildImageInfoMarkdownMetadata keeps image-derived text explicit for the LLM
// without introducing a second, user-visible markup protocol. Blockquotes keep
// multiline OCR attached to its image and remain harmless if copied verbatim.
func buildImageInfoMarkdownMetadata(img *types.ImageInfo) string {
	if img == nil {
		return ""
	}

	var lines []string
	if caption := strings.TrimSpace(img.Caption); caption != "" {
		lines = append(lines, "**Image caption:** "+caption)
	}
	if ocr := strings.TrimSpace(img.OCRText); ocr != "" {
		lines = append(lines, "**Image text (OCR):** "+ocr)
	}
	if len(lines) == 0 {
		return ""
	}

	return "> " + strings.ReplaceAll(strings.Join(lines, "\n\n"), "\n", "\n> ")
}

// BuildImageInfoMarkdownWithURL formats one image as answer-ready Markdown for
// LLM-facing chat/tool context. The URL is intentionally preserved verbatim;
// resource and provider URLs are opaque handles resolved by the frontend.
func BuildImageInfoMarkdownWithURL(url string, img *types.ImageInfo) string {
	if img == nil {
		return ""
	}
	url = strings.TrimSpace(url)
	metadata := buildImageInfoMarkdownMetadata(img)
	if url == "" {
		return metadata
	}

	alt := strings.Join(strings.Fields(img.Caption), " ")
	if alt == "" {
		alt = "image"
	}
	alt = strings.NewReplacer(
		`\`, `\\`,
		`[`, `\[`,
		`]`, `\]`,
	).Replace(alt)

	image := fmt.Sprintf("![%s](%s)", alt, url)
	if metadata == "" {
		return image
	}
	return image + "\n\n" + metadata
}

// BuildImageInfoXML returns XML-tagged caption / ocr for one image.
func BuildImageInfoXML(img *types.ImageInfo) string {
	var b strings.Builder
	if img.Caption != "" {
		b.WriteString(fmt.Sprintf("<image_caption>%s</image_caption>\n", img.Caption))
	}
	if img.OCRText != "" {
		b.WriteString(fmt.Sprintf("<image_ocr>%s</image_ocr>\n", img.OCRText))
	}
	return b.String()
}

// BuildImageInfoXMLWithURL wraps image info in an <image> element carrying the URL.
func BuildImageInfoXMLWithURL(url string, img *types.ImageInfo) string {
	inner := BuildImageInfoXML(img)
	if inner == "" {
		return ""
	}
	return fmt.Sprintf("<image url=\"%s\">\n%s</image>", url, inner)
}

// EnrichContentCaptionOnly is like EnrichContentWithImageInfo but only
// includes image captions (no OCR text). Original content (including Markdown
// image links) is preserved. Useful for summary generation where OCR would
// add too much noise.
func EnrichContentCaptionOnly(content string, imageInfoJSON string) string {
	var imageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfoJSON), &imageInfos); err != nil {
		return content
	}
	if len(imageInfos) == 0 {
		return content
	}

	imageInfoMap := make(map[string]*types.ImageInfo)
	for i := range imageInfos {
		if imageInfos[i].URL != "" {
			imageInfoMap[imageInfos[i].URL] = &imageInfos[i]
		}
		if imageInfos[i].OriginalURL != "" {
			imageInfoMap[imageInfos[i].OriginalURL] = &imageInfos[i]
		}
	}

	matches := MarkdownImageRegex.FindAllStringSubmatch(content, -1)
	processedURLs := make(map[string]bool)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		imgURL := match[2]
		processedURLs[imgURL] = true

		imgInfo, found := imageInfoMap[imgURL]
		if found && imgInfo != nil && imgInfo.Caption != "" {
			replacement := match[0] + "\n" + fmt.Sprintf("<image_caption>%s</image_caption>", imgInfo.Caption)
			content = strings.Replace(content, match[0], replacement, 1)
		}
	}

	var extras []string
	for _, imgInfo := range imageInfos {
		if processedURLs[imgInfo.URL] || processedURLs[imgInfo.OriginalURL] {
			continue
		}
		if imgInfo.Caption != "" {
			extras = append(extras, fmt.Sprintf("<image_caption>%s</image_caption>", imgInfo.Caption))
		}
	}
	if len(extras) > 0 {
		if content != "" {
			content += "\n"
		}
		content += strings.Join(extras, "\n")
	}
	return content
}

// EnrichContentCaptionAndOCR is like EnrichContentCaptionOnly but ALSO
// embeds OCR text alongside captions. URL and <image_original> wrapper
// blocks are deliberately omitted (unlike EnrichContentWithImageInfo) —
// the summary LLM only needs the human-readable text, not opaque export
// hashes. Used as a fallback for image-dominated documents where caption
// alone carries too little signal.
func EnrichContentCaptionAndOCR(content string, imageInfoJSON string) string {
	var imageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfoJSON), &imageInfos); err != nil {
		return content
	}
	if len(imageInfos) == 0 {
		return content
	}

	imageInfoMap := make(map[string]*types.ImageInfo)
	for i := range imageInfos {
		if imageInfos[i].URL != "" {
			imageInfoMap[imageInfos[i].URL] = &imageInfos[i]
		}
		if imageInfos[i].OriginalURL != "" {
			imageInfoMap[imageInfos[i].OriginalURL] = &imageInfos[i]
		}
	}

	matches := MarkdownImageRegex.FindAllStringSubmatch(content, -1)
	processedURLs := make(map[string]bool)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		imgURL := match[2]
		processedURLs[imgURL] = true

		imgInfo, found := imageInfoMap[imgURL]
		if !found || imgInfo == nil {
			continue
		}
		appended := buildCaptionOCRBlock(imgInfo)
		if appended == "" {
			continue
		}
		content = strings.Replace(content, match[0], match[0]+"\n"+appended, 1)
	}

	var extras []string
	for _, imgInfo := range imageInfos {
		if processedURLs[imgInfo.URL] || processedURLs[imgInfo.OriginalURL] {
			continue
		}
		if block := buildCaptionOCRBlock(&imgInfo); block != "" {
			extras = append(extras, block)
		}
	}
	if len(extras) > 0 {
		if content != "" {
			content += "\n"
		}
		content += strings.Join(extras, "\n")
	}
	return content
}

// buildCaptionOCRBlock returns the inline caption + OCR snippet (no URL
// wrapper) used by EnrichContentCaptionAndOCR. Empty string when the image
// has neither caption nor OCR.
func buildCaptionOCRBlock(img *types.ImageInfo) string {
	var parts []string
	if img.Caption != "" {
		parts = append(parts, fmt.Sprintf("<image_caption>%s</image_caption>", img.Caption))
	}
	if img.OCRText != "" {
		parts = append(parts, fmt.Sprintf("<image_ocr>%s</image_ocr>", img.OCRText))
	}
	return strings.Join(parts, "\n")
}
