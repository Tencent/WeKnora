package service

const (
	// defaultWikiContentSegments is the Pass 0 window cap when the KB's
	// WikiConfig does not set IngestMaxContentSegments. 8 windows of
	// maxContentForWiki runes cover ~256k runes of document text.
	defaultWikiContentSegments = 8

	// wikiContentSegmentOverlap is how many runes consecutive windows share,
	// so an entity introduced across a window boundary is seen whole by at
	// least one extraction call.
	wikiContentSegmentOverlap = 1024
)

// contentSegmentLimit returns the Pass 0 window cap for this batch.
func (c *WikiBatchContext) contentSegmentLimit() int {
	if c == nil || c.MaxContentSegments <= 0 {
		return defaultWikiContentSegments
	}
	return c.MaxContentSegments
}

// splitWikiContent cuts content into windows of at most size runes, each
// sharing overlap runes with the previous one. A window end is pulled back
// to the last newline inside the overlap zone when there is one, so cuts
// tend to land between paragraphs. At most maxSegments windows are
// returned; truncated reports whether text past the last window was left
// out. Content that fits in one window comes back unchanged.
func splitWikiContent(content string, size, overlap, maxSegments int) (segments []string, truncated bool) {
	runes := []rune(content)
	if size <= 0 || len(runes) <= size {
		return []string{content}, false
	}
	if overlap < 0 || overlap >= size/2 {
		overlap = 0
	}
	if maxSegments <= 0 {
		maxSegments = 1
	}

	start := 0
	for {
		end := start + size
		if end >= len(runes) {
			return append(segments, string(runes[start:])), false
		}
		for i := end - 1; i >= end-overlap; i-- {
			if runes[i] == '\n' {
				end = i + 1
				break
			}
		}
		segments = append(segments, string(runes[start:end]))
		if len(segments) == maxSegments {
			return segments, true
		}
		start = end - overlap
	}
}
