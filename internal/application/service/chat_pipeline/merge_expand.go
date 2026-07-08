package chatpipeline

import (
	"context"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	markdownImagePattern      = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	figureContextAfterImageRE = regexp.MustCompile(`图\s*[0-9]+[-－—][0-9]+(?:[-－—][0-9]+)?|图点评|患者|说明[:：]|名称[:：]|编号[:：]`)
)

// expandShortContextWithNeighbors expands short contexts and dangling empty-alt
// figure contexts with neighboring chunks.
func (p *PluginMerge) expandShortContextWithNeighbors(
	ctx context.Context,
	chatManage *types.ChatManage,
	results []*types.SearchResult,
) []*types.SearchResult {
	const (
		minLen                  = 350
		maxLen                  = 850
		danglingImageNextMaxLen = 1000
	)

	if len(results) == 0 || p.chunkRepo == nil {
		return results
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	if tenantID == 0 && chatManage != nil {
		tenantID = chatManage.TenantID
	}
	if tenantID == 0 {
		pipelineWarn(ctx, "Merge", "expand_skip", map[string]interface{}{
			"reason": "missing_tenant",
		})
		return results
	}

	type targetInfo struct {
		result             *types.SearchResult
		danglingEmptyImage bool
	}

	targets := make([]targetInfo, 0)
	baseIDsSet := make(map[string]struct{})

	for _, r := range results {
		if r == nil || r.ID == "" || r.Content == "" {
			continue
		}
		if r.ChunkType != string(types.ChunkTypeText) {
			continue
		}
		danglingEmptyImage := hasDanglingEmptyAltImage(r.Content)
		if runeLen(r.Content) >= minLen && !danglingEmptyImage {
			continue
		}
		targets = append(targets, targetInfo{
			result:             r,
			danglingEmptyImage: danglingEmptyImage,
		})
		baseIDsSet[r.ID] = struct{}{}
		pipelineInfo(ctx, "Merge", "need_expand", map[string]interface{}{
			"chunk_id":             r.ID,
			"content":              r.Content,
			"chunk_type":           r.ChunkType,
			"len":                  runeLen(r.Content),
			"dangling_empty_image": danglingEmptyImage,
		})
	}

	if len(targets) == 0 {
		return results
	}

	baseIDs := make([]string, 0, len(baseIDsSet))
	for id := range baseIDsSet {
		baseIDs = append(baseIDs, id)
	}

	chunkMap := make(map[string]*types.Chunk, len(baseIDs))
	chunks, err := p.chunkRepo.ListChunksByID(ctx, tenantID, baseIDs)
	if err != nil {
		pipelineWarn(ctx, "Merge", "expand_list_base_failed", map[string]interface{}{
			"error": err.Error(),
		})
		return results
	}
	for _, chunk := range chunks {
		chunkMap[chunk.ID] = chunk
	}

	neighborIDsSet := make(map[string]struct{})
	for _, chunk := range chunkMap {
		if chunk == nil {
			continue
		}
		if chunk.PreChunkID != "" {
			if _, exists := chunkMap[chunk.PreChunkID]; !exists {
				neighborIDsSet[chunk.PreChunkID] = struct{}{}
			}
		}
		if chunk.NextChunkID != "" {
			if _, exists := chunkMap[chunk.NextChunkID]; !exists {
				neighborIDsSet[chunk.NextChunkID] = struct{}{}
			}
		}
	}

	if len(neighborIDsSet) > 0 {
		neighborIDs := make([]string, 0, len(neighborIDsSet))
		for id := range neighborIDsSet {
			neighborIDs = append(neighborIDs, id)
		}
		neighbors, err := p.chunkRepo.ListChunksByID(ctx, tenantID, neighborIDs)
		if err != nil {
			pipelineWarn(ctx, "Merge", "expand_list_neighbor_failed", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			for _, chunk := range neighbors {
				chunkMap[chunk.ID] = chunk
				pipelineInfo(ctx, "Merge", "expand_list_neighbor_success", map[string]interface{}{
					"neighbor_chunk_id":   chunk.ID,
					"neighbor_content":    chunk.Content,
					"neighbor_chunk_type": chunk.ChunkType,
					"neighbor_len":        runeLen(chunk.Content),
				})
			}
		}
	}

	for _, target := range targets {
		res := target.result
		p.fetchChunksIfMissing(ctx, tenantID, chunkMap, res.ID)
		baseChunk := chunkMap[res.ID]
		if baseChunk == nil || baseChunk.Content == "" || baseChunk.ChunkType != types.ChunkTypeText {
			continue
		}

		if target.danglingEmptyImage && runeLen(baseChunk.Content) >= minLen {
			p.expandDanglingEmptyAltImageWithNext(
				ctx,
				tenantID,
				res,
				baseChunk,
				chunkMap,
				danglingImageNextMaxLen,
			)
			continue
		}

		prevContent := ""
		nextContent := ""
		prevIDs := []string{}
		nextIDs := []string{}

		prevCursor := baseChunk.PreChunkID
		nextCursor := baseChunk.NextChunkID

		p.fetchChunksIfMissing(ctx, tenantID, chunkMap, prevCursor, nextCursor)

		if prevCursor != "" {
			if prevChunk := chunkMap[prevCursor]; prevChunk != nil && prevChunk.KnowledgeID == baseChunk.KnowledgeID {
				prevContent = prevChunk.Content
				prevIDs = append(prevIDs, prevChunk.ID)
				prevCursor = prevChunk.PreChunkID
			} else {
				prevCursor = ""
			}
		}

		if nextCursor != "" {
			if nextChunk := chunkMap[nextCursor]; nextChunk != nil && nextChunk.KnowledgeID == baseChunk.KnowledgeID {
				nextContent = nextChunk.Content
				nextIDs = append(nextIDs, nextChunk.ID)
				nextCursor = nextChunk.NextChunkID
			} else {
				nextCursor = ""
			}
		}

		var merged string
		for {
			merged = mergeOrderedContent(prevContent, baseChunk.Content, nextContent, maxLen)
			if merged == "" {
				break
			}
			if runeLen(merged) >= minLen {
				break
			}
			if prevCursor == "" && nextCursor == "" {
				break
			}

			expanded := false
			if prevCursor != "" {
				p.fetchChunksIfMissing(ctx, tenantID, chunkMap, prevCursor)
				if prevChunk := chunkMap[prevCursor]; prevChunk != nil &&
					prevChunk.KnowledgeID == baseChunk.KnowledgeID {
					prevContent = concatNoOverlap(prevChunk.Content, prevContent)
					prevIDs = append([]string{prevChunk.ID}, prevIDs...)
					prevCursor = prevChunk.PreChunkID
					expanded = true
				} else {
					prevCursor = ""
				}
			}

			merged = mergeOrderedContent(prevContent, baseChunk.Content, nextContent, maxLen)
			if runeLen(merged) >= minLen {
				break
			}

			if nextCursor != "" {
				p.fetchChunksIfMissing(ctx, tenantID, chunkMap, nextCursor)
				if nextChunk := chunkMap[nextCursor]; nextChunk != nil &&
					nextChunk.KnowledgeID == baseChunk.KnowledgeID {
					nextContent = concatNoOverlap(nextContent, nextChunk.Content)
					nextIDs = append(nextIDs, nextChunk.ID)
					nextCursor = nextChunk.NextChunkID
					expanded = true
				} else {
					nextCursor = ""
				}
			}

			if !expanded {
				break
			}
		}

		if merged == "" {
			continue
		}

		beforeLen := runeLen(res.Content)
		res.Content = merged

		for _, id := range prevIDs {
			if id != "" && !containsID(res.SubChunkID, id) {
				res.SubChunkID = append(res.SubChunkID, id)
			}
		}
		for _, id := range nextIDs {
			if id != "" && !containsID(res.SubChunkID, id) {
				res.SubChunkID = append(res.SubChunkID, id)
			}
		}

		if prevContent != "" {
			res.StartAt = baseChunk.StartAt - runeLen(prevContent)
			if res.StartAt < 0 {
				res.StartAt = 0
			}
		}
		res.EndAt = res.StartAt + runeLen(res.Content)

		pipelineInfo(ctx, "Merge", "expand_short_chunk", map[string]interface{}{
			"chunk_id":       res.ID,
			"prev_ids":       prevIDs,
			"next_ids":       nextIDs,
			"before_len":     beforeLen,
			"after_len":      runeLen(res.Content),
			"base_content":   baseChunk.Content,
			"after_content":  res.Content,
			"chunk_type":     res.ChunkType,
			"remaining_prev": prevCursor,
			"remaining_next": nextCursor,
		})
	}

	return results
}

func (p *PluginMerge) expandDanglingEmptyAltImageWithNext(
	ctx context.Context,
	tenantID uint64,
	res *types.SearchResult,
	baseChunk *types.Chunk,
	chunkMap map[string]*types.Chunk,
	nextMaxLen int,
) {
	if res == nil || baseChunk == nil || baseChunk.NextChunkID == "" {
		return
	}

	p.fetchChunksIfMissing(ctx, tenantID, chunkMap, baseChunk.NextChunkID)
	nextChunk := chunkMap[baseChunk.NextChunkID]
	if nextChunk == nil ||
		nextChunk.KnowledgeID != baseChunk.KnowledgeID ||
		nextChunk.Content == "" ||
		nextChunk.ChunkType != types.ChunkTypeText {
		return
	}

	nextContent := trimRunes(nextChunk.Content, nextMaxLen)
	if nextContent == "" {
		return
	}

	beforeLen := runeLen(res.Content)
	res.Content = concatNoOverlap(baseChunk.Content, nextContent)
	if !containsID(res.SubChunkID, nextChunk.ID) {
		res.SubChunkID = append(res.SubChunkID, nextChunk.ID)
	}
	res.StartAt = baseChunk.StartAt
	res.EndAt = res.StartAt + runeLen(res.Content)

	pipelineInfo(ctx, "Merge", "expand_dangling_empty_alt_image", map[string]interface{}{
		"chunk_id":      res.ID,
		"next_id":       nextChunk.ID,
		"before_len":    beforeLen,
		"after_len":     runeLen(res.Content),
		"next_len":      runeLen(nextContent),
		"base_content":  baseChunk.Content,
		"after_content": res.Content,
		"chunk_type":    res.ChunkType,
	})
}

func hasDanglingEmptyAltImage(content string) bool {
	matches := markdownImagePattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return false
	}

	last := matches[len(matches)-1]
	if len(last) < 4 || last[2] < 0 || last[3] < 0 {
		return false
	}
	if strings.TrimSpace(content[last[2]:last[3]]) != "" {
		return false
	}

	afterImage := strings.TrimSpace(content[last[1]:])
	if afterImage != "" && figureContextAfterImageRE.MatchString(afterImage) {
		return false
	}

	return runeLen(content[last[0]:]) <= 500
}

func trimRunes(content string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(content)
	if len(runes) <= maxLen {
		return content
	}
	return string(runes[:maxLen])
}

// runeLen returns the length of a string in runes
func runeLen(s string) int {
	return len([]rune(s))
}

// mergeOrderedContent merges ordered content
func mergeOrderedContent(prev, base, next string, maxLen int) string {
	content := base
	if prev != "" {
		content = concatNoOverlap(prev, content)
	}
	if next != "" {
		content = concatNoOverlap(content, next)
	}
	runes := []rune(content)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return content
}

// concatNoOverlap concatenates two strings, removing potential overlapping prefix/suffix
func concatNoOverlap(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}

	ar := []rune(a)
	br := []rune(b)
	maxOverlap := minInt(len(ar), len(br))
	for k := maxOverlap; k > 0; k-- {
		if string(ar[len(ar)-k:]) == string(br[:k]) {
			return string(ar) + string(br[k:])
		}
	}
	return string(ar) + string(br)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func (p *PluginMerge) fetchChunksIfMissing(
	ctx context.Context,
	tenantID uint64,
	chunkMap map[string]*types.Chunk,
	chunkIDs ...string,
) {
	missing := make([]string, 0, len(chunkIDs))
	for _, id := range chunkIDs {
		if id == "" {
			continue
		}
		if _, exists := chunkMap[id]; !exists {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return
	}

	chunks, err := p.chunkRepo.ListChunksByID(ctx, tenantID, missing)
	if err != nil {
		pipelineWarn(ctx, "Merge", "expand_fetch_missing_failed", map[string]interface{}{
			"missing_cnt": len(missing),
			"error":       err.Error(),
		})
	}

	found := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		chunkMap[chunk.ID] = chunk
		found[chunk.ID] = struct{}{}
	}

	for _, id := range missing {
		if _, ok := found[id]; !ok {
			chunkMap[id] = nil
		}
	}
}
