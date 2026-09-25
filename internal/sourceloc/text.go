package sourceloc

import "github.com/Tencent/WeKnora/internal/types"

// TextBlocks splits a plain-text original (Markdown, TXT) that the parser
// passed through unchanged into paragraph blocks whose text locators point at
// the same rune ranges of the original file.
func TextBlocks(content string) []types.SourceBlock {
	var blocks []types.SourceBlock
	start := -1 // rune offset where the current paragraph starts
	pos := 0
	blankRun := 0
	emit := func(end int) {
		if start >= 0 && end > start {
			blocks = append(blocks, types.SourceBlock{
				Start: start, End: end,
				Locator: types.SourceLocator{Type: types.SourceLocatorText, Start: start, End: end},
			})
		}
		start = -1
	}
	lineHasText := false
	lineStart := 0
	for _, r := range content {
		switch r {
		case '\n':
			if !lineHasText {
				blankRun++
				if blankRun == 1 {
					emit(lineStart)
				}
			} else {
				blankRun = 0
			}
			lineHasText = false
			lineStart = pos + 1
		case ' ', '\t', '\r':
		default:
			if !lineHasText && start < 0 {
				start = lineStart
			}
			lineHasText = true
		}
		pos++
	}
	emit(pos)
	return blocks
}
