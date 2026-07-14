package searchutil

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// SliceContentByDocumentRange extracts the substring of content whose
// document-level rune offsets fall within [rangeStart, rangeEnd).
// contentStartAt is the document offset where content begins.
func SliceContentByDocumentRange(content string, contentStartAt, rangeStart, rangeEnd int) string {
	if content == "" {
		return content
	}
	relStart := rangeStart - contentStartAt
	relEnd := rangeEnd - contentStartAt
	runes := []rune(content)
	if relStart < 0 {
		relStart = 0
	}
	if relEnd > len(runes) {
		relEnd = len(runes)
	}
	if relStart >= relEnd {
		return ""
	}
	return string(runes[relStart:relEnd])
}

// ImageURLsInContent returns the set of image URLs referenced as Markdown
// images in content.
func ImageURLsInContent(content string) map[string]bool {
	urls := make(map[string]bool)
	for _, match := range MarkdownImageRegex.FindAllStringSubmatch(content, -1) {
		if len(match) < 3 || match[2] == "" {
			continue
		}
		urls[match[2]] = true
	}
	return urls
}

// FilterImageInfoByContentURLs keeps only image_info entries whose URL or
// OriginalURL appears as a Markdown image in content. Returns empty string
// when nothing matches or imageInfoJSON is invalid.
func FilterImageInfoByContentURLs(content string, imageInfoJSON string) string {
	if imageInfoJSON == "" {
		return ""
	}
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfoJSON), &infos); err != nil || len(infos) == 0 {
		return ""
	}
	urls := ImageURLsInContent(content)
	if len(urls) == 0 {
		return ""
	}
	filtered := make([]types.ImageInfo, 0, len(infos))
	for _, info := range infos {
		if urls[info.URL] || urls[info.OriginalURL] {
			filtered = append(filtered, info)
		}
	}
	return marshalImageInfos(filtered)
}

// FilterImageInfoByMatchRange keeps image_info entries whose Markdown image
// reference falls within the document rune range [matchStart, matchEnd).
// Used when parent content is expanded for context but multimodal enrichment
// should only cover the retrieved child window.
func FilterImageInfoByMatchRange(
	parentContent string,
	parentStartAt, matchStart, matchEnd int,
	imageInfoJSON string,
) string {
	if imageInfoJSON == "" {
		return ""
	}
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfoJSON), &infos); err != nil || len(infos) == 0 {
		return ""
	}
	window := SliceContentByDocumentRange(parentContent, parentStartAt, matchStart, matchEnd)
	urls := ImageURLsInContent(window)
	if len(urls) == 0 {
		return ""
	}
	filtered := make([]types.ImageInfo, 0, len(infos))
	for _, info := range infos {
		if urls[info.URL] || urls[info.OriginalURL] {
			filtered = append(filtered, info)
		}
	}
	return marshalImageInfos(filtered)
}

func marshalImageInfos(infos []types.ImageInfo) string {
	if len(infos) == 0 {
		return ""
	}
	data, err := json.Marshal(infos)
	if err != nil {
		return ""
	}
	return string(data)
}

// PruneMarkdownImagesOutsideRange removes Markdown image lines whose
// document-level rune offsets fall outside [matchStart, matchEnd). Non-image
// text is preserved so parent-child expansion still provides full textual
// context while dropping irrelevant page thumbnails.
func PruneMarkdownImagesOutsideRange(
	content string,
	contentStartAt, matchStart, matchEnd int,
) string {
	locs := MarkdownImageRegex.FindAllStringIndex(content, -1)
	if len(locs) == 0 {
		return content
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		if loc[0] < last || loc[1] > len(content) {
			continue
		}
		docStart := contentStartAt + utf8.RuneCountInString(content[:loc[0]])
		docEnd := contentStartAt + utf8.RuneCountInString(content[:loc[1]])
		inRange := docStart < matchEnd && docEnd > matchStart
		if inRange {
			b.WriteString(content[last:loc[1]])
		} else {
			b.WriteString(content[last:loc[0]])
		}
		last = loc[1]
	}
	b.WriteString(content[last:])
	return collapseBlankLines(b.String())
}

func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}
