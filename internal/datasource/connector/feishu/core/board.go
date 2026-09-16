package core

import (
	"context"
	"fmt"
	"net/url"
)

// board.go covers the docx board block (block_type 43): whiteboards embedded in
// documents are exported to an image via the official download_as_image API and
// ride the embedded-image pipeline as a regular image sub-item
// (external_id "<parent>#board#<token>").
//
// API: GET /open-apis/board/v1/whiteboards/:whiteboard_id/download_as_image —
// responds with the raw image bytes (Content-Type image/png|jpeg|gif|svg+xml).
// Scope: board:whiteboard:node:read. 403 (code 2890005) means the app lacks
// read permission on the board; the caller degrades to a Markdown note rather
// than failing the document.

// downloadBoardAsImage fetches a whiteboard's thumbnail image bytes. Transient
// failures (429/5xx/transport) retry via the shared downloadRawBytes policy;
// the 512 MB maxFeishuDownloadBytes cap applies too (a real whiteboard PNG is
// orders of magnitude smaller).
func (c *Client) downloadBoardAsImage(ctx context.Context, boardToken string) ([]byte, error) {
	path := fmt.Sprintf("/open-apis/board/v1/whiteboards/%s/download_as_image", url.PathEscape(boardToken))
	return c.downloadRawBytes(ctx, path)
}
