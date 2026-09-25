package service

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/sourceloc"
	"github.com/Tencent/WeKnora/internal/types"
)

// attachStructureBlocks gives a parse result source blocks when the parser
// reported none: office files are aligned against their own structure, and
// plain-text files that the parser passed through verbatim map onto
// themselves. Failures only cost the locators, never the parse.
func attachStructureBlocks(ctx context.Context, fileType string, data []byte, result *types.ReadResult) {
	if result == nil || len(result.SourceBlocks) > 0 || result.MarkdownContent == "" || len(data) == 0 {
		return
	}
	ft := strings.ToLower(strings.TrimPrefix(fileType, "."))
	switch {
	case sourceloc.SupportsStructure(ft):
		started := time.Now()
		blocks, err := sourceloc.AlignStructure(ft, data, result.MarkdownContent)
		if err != nil {
			logger.Warnf(ctx, "[SourceLocator] reading %s structure failed: %v", ft, err)
			return
		}
		result.SourceBlocks = blocks
		logger.Infof(ctx, "[SourceLocator] aligned %d %s blocks in %s", len(blocks), ft, time.Since(started))
	case ft == "md" || ft == "markdown" || ft == "txt" || ft == "text":
		if result.MarkdownContent == string(data) {
			result.SourceBlocks = passThroughTextBlocks(result.MarkdownContent)
		}
	}
}

// passThroughTextBlocks maps a verbatim text original onto itself. Text
// locators count from after a UTF-8 BOM, since viewers decode the file
// without it; the blocks still cover the markdown, BOM included.
func passThroughTextBlocks(content string) []types.SourceBlock {
	body, hasBOM := strings.CutPrefix(content, "\ufeff")
	blocks := sourceloc.TextBlocks(body)
	if hasBOM {
		for i := range blocks {
			blocks[i].Start++
			blocks[i].End++
		}
	}
	return blocks
}

// transcriptWithSegments renders an ASR result with one line per timed
// segment, and the time blocks that point each line back into the audio.
// Without segments the plain transcript is returned and no blocks.
func transcriptWithSegments(result *asr.TranscriptionResult) (string, []types.SourceBlock) {
	if result == nil {
		return "", nil
	}
	if len(result.Segments) == 0 {
		return result.Text, nil
	}
	var (
		sb     strings.Builder
		blocks []types.SourceBlock
		pos    int
	)
	for _, seg := range result.Segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
			pos++
		}
		n := len([]rune(text))
		sb.WriteString(text)
		blocks = append(blocks, types.SourceBlock{
			Start: pos, End: pos + n,
			Locator: types.SourceLocator{
				Type:    types.SourceLocatorTime,
				StartMs: int64(seg.Start * 1000),
				EndMs:   int64(seg.End * 1000),
			},
		})
		pos += n
	}
	if len(blocks) == 0 {
		return result.Text, nil
	}
	return sb.String(), blocks
}

// buildSourceIndex re-aims the parser's source blocks at the markdown the
// chunker will split, and places every stored image in the original file.
// parsed is the markdown the blocks were recorded against.
func buildSourceIndex(
	parsed string, blocks []types.SourceBlock, final string, images []docparser.StoredImage,
) *sourceloc.Index {
	if len(blocks) == 0 {
		return nil
	}
	idx := sourceloc.NewIndex(final, sourceloc.RemapBlocks(blocks, parsed, final))
	if idx == nil {
		return nil
	}
	// Visit the images in document order so rune offsets are counted in one
	// pass over the markdown instead of once per image.
	type placed struct{ image, at int }
	var found []placed
	for i := range images {
		if images[i].ServingURL == "" {
			continue
		}
		if at := strings.Index(final, images[i].ServingURL); at >= 0 {
			found = append(found, placed{image: i, at: at})
		}
	}
	sort.Slice(found, func(a, b int) bool { return found[a].at < found[b].at })
	bytePos, runePos := 0, 0
	for _, f := range found {
		runePos += utf8.RuneCountInString(final[bytePos:f.at])
		bytePos = f.at
		images[f.image].SourceLocators = idx.LocatorsAt(runePos)
	}
	return idx
}
