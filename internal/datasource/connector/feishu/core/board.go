package core

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"unicode"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// downloadWhiteboardAsImage fetches the thumbnail PNG/JPEG for one embedded
// whiteboard (docx block_type 43). Must be read as binary — Feishu may still
// return a JSON error body on some failures.
func (c *Client) downloadWhiteboardAsImage(ctx context.Context, whiteboardID string) ([]byte, error) {
	path := fmt.Sprintf("/open-apis/board/v1/whiteboards/%s/download_as_image",
		url.PathEscape(whiteboardID))
	data, err := c.downloadRawBytes(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(data) < 100 {
		return nil, fmt.Errorf("whiteboard %s image too small (%d bytes)", whiteboardID, len(data))
	}
	if looksLikeJSONObject(data) {
		return nil, fmt.Errorf("whiteboard %s download returned JSON error body: %s",
			whiteboardID, truncate(string(data), 300))
	}
	return data, nil
}

func looksLikeJSONObject(data []byte) bool {
	i := 0
	for i < len(data) && unicode.IsSpace(rune(data[i])) {
		i++
	}
	return i < len(data) && data[i] == '{'
}

// appendBoardItems fans embedded whiteboard blocks out as image knowledge
// sub-items (same metadata contract as BlockTypeImage). Failures warn and
// continue; they must not fail the parent document.
func appendBoardItems(ctx context.Context, client *Client, in DocxFetchInput, blocks []DocxBlock, items []*types.FetchedItem, keep []string) ([]*types.FetchedItem, []string) {
	imgMeta := func() map[string]string {
		m := maps.Clone(in.BaseMeta)
		if m == nil {
			m = map[string]string{}
		}
		m["parent_node_token"] = in.DocToken
		m["embedded_image"] = "true"
		m["whiteboard"] = "true"
		return m
	}
	for _, b := range blocks {
		wid := boardToken(b)
		if wid == "" {
			continue
		}
		childID := types.SubtreeChildID(in.DocToken, "board", wid)
		keep = append(keep, childID)
		if !in.MultimodalEnabled {
			continue
		}
		data, derr := client.downloadWhiteboardAsImage(ctx, wid)
		if derr != nil {
			logger.Warnf(ctx, "[Feishu] doc %s: whiteboard %s download failed: %v",
				in.ObjToken, wid, derr)
			items = append(items, &types.FetchedItem{
				ExternalID:       childID,
				Title:            fmt.Sprintf("%s（内嵌画板）", in.Title),
				SourceResourceID: in.ResourceID,
				Metadata:         FeishuErrorItemMeta(derr, imgMeta()),
			})
			continue
		}
		cropped, cerr := AutocropImage(data)
		if cerr != nil {
			logger.Warnf(ctx, "[Feishu] doc %s: whiteboard %s crop failed, using original: %v",
				in.ObjToken, wid, cerr)
			cropped = data
		}
		if len(cropped) < 100 {
			continue
		}
		ext, contentType, ok := SupportedImageExt(cropped)
		if !ok {
			logger.Warnf(ctx, "[Feishu] doc %s: skipping whiteboard %s of unsupported type %q",
				in.ObjToken, wid, contentType)
			continue
		}
		items = append(items, &types.FetchedItem{
			ExternalID:       childID,
			Title:            fmt.Sprintf("%s（内嵌画板）", in.Title),
			Content:          cropped,
			ContentType:      contentType,
			FileName:         NestedFileName(in.Title, BoardRelName(wid, ext)),
			URL:              in.URL,
			UpdatedAt:        in.EditTime,
			SourceResourceID: in.ResourceID,
			Metadata:         imgMeta(),
		})
	}
	return items, keep
}

func boardToken(b DocxBlock) string {
	if b.BlockType != BlockTypeBoard {
		return ""
	}
	if b.Board != nil && b.Board.Token != "" {
		return b.Board.Token
	}
	return ""
}
